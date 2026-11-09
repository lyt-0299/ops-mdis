package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusProcessing TaskStatus = "processing"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
)

const (
	CodeSuccess           = 0 // 成功
	CodeTaskAlreadyExists = 1 // 任务已存在
	CodeDatabaseError     = 2 // 数据库错误
	CodeInvalidRequest    = 3 // 无效请求
	CodeIndexInProgress   = 4 // 索引正在创建中
	CodeTaskFailed        = 5 // 任务失败
)

type IndexTask struct {
	ID          string     `json:"id"`
	DbTag       string     `json:"dbTag"`
	DbName      string     `json:"dbname"`
	WorksheetID string     `json:"worksheetID"`
	ControlID   string     `json:"controlID"`
	Status      TaskStatus `json:"status"`
	Error       string     `json:"error,omitempty"`
}

type TaskQueue struct {
	mu    sync.Mutex
	tasks []IndexTask
}

func (q *TaskQueue) AddTask(task IndexTask) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, task)
}

func (q *TaskQueue) GetTasks() []IndexTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]IndexTask{}, q.tasks...)
}

func (q *TaskQueue) TaskExists(worksheetID, controlID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, task := range q.tasks {
		if task.WorksheetID == worksheetID && task.ControlID == controlID {
			return true
		}
	}
	return false
}

func (q *TaskQueue) UpdateTaskStatus(id string, status TaskStatus, err string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.tasks {
		if q.tasks[i].ID == id {
			q.tasks[i].Status = status
			q.tasks[i].Error = err
			break
		}
	}
}

func (q *TaskQueue) RemoveTask(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, task := range q.tasks {
		if task.ID == id {
			q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
			break
		}
	}
}

var (
	taskQueue   = TaskQueue{}
	taskQueueCh = make(chan IndexTask, 300)
	taskQueueWg sync.WaitGroup
)

func init() {
	go processQueue()
}

func processQueue() {
	for task := range taskQueueCh {
		taskQueue.UpdateTaskStatus(task.ID, StatusProcessing, "")

		// 更新状态到 slowops 集合
		updateIndexStatus("processing", task, "")

		clientInfo, err := getClient(task.DbTag)
		if err != nil {
			taskQueue.UpdateTaskStatus(task.ID, StatusFailed, err.Error())
			updateIndexStatus("failed", task, err.Error())
			taskQueue.RemoveTask(task.ID)
			continue
		}
		db := clientInfo.Client.Database(task.DbName)
		collection := db.Collection("ws" + task.WorksheetID)

		indexModel := mongo.IndexModel{
			Keys: bson.D{{Key: task.ControlID, Value: 1}},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		_, err = collection.Indexes().CreateOne(ctx, indexModel)
		if err != nil {
			taskQueue.UpdateTaskStatus(task.ID, StatusFailed, err.Error())
			updateIndexStatus("failed", task, err.Error())
			taskQueue.RemoveTask(task.ID) // 从队列中移除
			continue
		}

		taskQueue.UpdateTaskStatus(task.ID, StatusCompleted, "")
		updateIndexStatus("completed", task, "")
		taskQueue.RemoveTask(task.ID) // 从队列中移除
	}
	taskQueueWg.Done()
}

func updateIndexStatus(status string, task IndexTask, errMsg string) {
	// 索引任务状态回写到慢查询历史(ops-mongo)，不写被监控目标库。
	slowopsCollection, err := slowOpsColl()
	if err != nil {
		fmt.Printf("Failed to update index status in slow query history: %v\n", err)
		return
	}

	filter := bson.M{"controlInfo.controlid": task.ControlID, "controlInfo.worksheetid": task.WorksheetID}
	update := bson.M{
		"$set": bson.M{
			"controlInfo.indexStatus": bson.M{
				"status":    status,
				"timestamp": time.Now(),
				"error":     errMsg,
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = slowopsCollection.UpdateMany(ctx, filter, update)
	if err != nil {
		fmt.Printf("Failed to update index status in slowops: %v\n", err)
	}
}

func checkDatabaseStatus(worksheetID, controlID string) (bool, error) {
	// 读慢查询历史里的索引状态(ops-mongo)。
	slowopsCollection, err := slowOpsColl()
	if err != nil {
		return false, err
	}

	filter := bson.M{"controlInfo.worksheetid": worksheetID, "controlInfo.controlid": controlID}

	var result struct {
		ControlInfo struct {
			IndexStatus struct {
				Status string `bson:"status"`
			} `bson:"indexStatus"`
		} `bson:"controlInfo"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = slowopsCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return true, nil
		}
		return false, err
	}

	return result.ControlInfo.IndexStatus.Status == "", nil
}

func CreateIndexHandler(w http.ResponseWriter, r *http.Request) {
	type IndexCreateRequest struct {
		DbName      string `json:"dbname"`
		WorksheetID string `json:"worksheetID"`
		ControlID   string `json:"controlID"`
		DbTag       string `json:"dbTag"`
	}

	if r.Method != http.MethodPost {
		http.Error(w, jsonError("Invalid GlobalArgs method"), http.StatusMethodNotAllowed)
		return
	}

	if !hasSlowQueryClients() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(jsonError("慢查询目标 MongoDB 未配置，索引功能暂不可用")))
		return
	}

	var request IndexCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, jsonError(err.Error()), http.StatusBadRequest)
		return
	}

	if request.DbTag == "" {
		request.DbTag = "default"
	}

	if request.WorksheetID == "" || request.ControlID == "" {
		http.Error(w, jsonError("WorksheetID and ControlID are required"), http.StatusBadRequest)
		return
	}

	if taskQueue.TaskExists(request.WorksheetID, request.ControlID) {
		// 任务已存在
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Task    IndexTask   `json:"task,omitempty"`
			Queue   []IndexTask `json:"queue,omitempty"`
		}{
			Code:    CodeTaskAlreadyExists,
			Message: "Task already exists in the queue",
			Queue:   taskQueue.GetTasks(),
		})
		return
	}

	isUncreated, err := checkDatabaseStatus(request.WorksheetID, request.ControlID)
	if err != nil {
		// 数据库错误
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Task    IndexTask   `json:"task,omitempty"`
			Queue   []IndexTask `json:"queue,omitempty"`
		}{
			Code:    CodeDatabaseError,
			Message: fmt.Sprintf("Database error: %v", err),
		})
		return
	}
	if !isUncreated {
		// 索引已创建或正在创建
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Task    IndexTask   `json:"task,omitempty"`
			Queue   []IndexTask `json:"queue,omitempty"`
		}{
			Code:    CodeIndexInProgress,
			Message: "Index already created or in progress",
		})
		return
	}

	task := IndexTask{
		ID:          fmt.Sprintf("%d", time.Now().UnixNano()),
		DbTag:       request.DbTag,
		DbName:      request.DbName,
		WorksheetID: request.WorksheetID,
		ControlID:   request.ControlID,
		Status:      StatusPending,
	}

	// 将任务添加到队列
	taskQueue.AddTask(task)
	taskQueueCh <- task

	// 更新数据库中的索引状态为 pending
	updateIndexStatus("pending", task, "")

	// 返回成功的响应
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Code    int         `json:"code"`
		Message string      `json:"message"`
		Task    IndexTask   `json:"task"`
		Queue   []IndexTask `json:"queue"`
	}{
		Code:    CodeSuccess,
		Message: "Task added to queue",
		Task:    task,
		Queue:   taskQueue.GetTasks(),
	})
}

// jsonError 用于生成 JSON 格式的错误响应
func jsonError(message string) string {
	errorResponse := struct {
		Error string `json:"error"`
	}{message}

	jsonData, _ := json.Marshal(errorResponse)
	return string(jsonData)
}
