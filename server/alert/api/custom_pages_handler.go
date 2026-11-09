package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

// 自定义页面：把任意一个已有 Web UI(Flink / Langfuse / 内部看板…)挂进左侧菜单。
// 存于 alert_config(ops-mongo)，一条一个文档：
//
//	{_id:<slug>, kind:"customPage", name, url, mode, enabled, injectCss?, createdAt, updatedAt}
//
// 两种嵌法(mode)：
//   - proxy  经 gateway 反代(${BASE}/custom/<slug>/ → ops-server → 目标)。同源、带平台鉴权，
//     适合资源用相对路径的应用(Flink 即是)。
//   - iframe 前端 iframe 直接指向目标地址。不经平台，简单；但目标站得允许被嵌入
//     (没有 X-Frame-Options / frame-ancestors 限制)，且资源用绝对路径的应用
//     (Next.js 那种 /_next/*，如 Langfuse)只能走这条。
//
// 1.5.12 之前这里只有一个写死的 Flink 单例(kind:"flinkConfig")，见 migrateFlinkConfig。
const (
	kindCustomPage = "customPage"

	customPageModeProxy  = "proxy"
	customPageModeIframe = "iframe"
)

// flinkInjectCSS 是原先由网关 sub_filter 注入的 flink/custem.css：
// 把 Flink 左侧栏改成白底黑字，跟平台配色对齐。迁移时写进 flink 那条记录的 injectCss，
// 反代时由 ops-server 注入 —— 网关不再需要 Flink 专属的 location 与 css 文件。
const flinkInjectCSS = `.ant-layout-sider-children {
    background-color: white;
    color: black;
}

.ant-layout-sider-children * {
    background-color: white !important;
    color: black !important;
}`

// customPageDoc 是 alert_config 里一条自定义页面的存储形态。
type customPageDoc struct {
	ID        string    `bson:"_id"`
	Kind      string    `bson:"kind"`
	Name      string    `bson:"name"`
	URL       string    `bson:"url"`
	Mode      string    `bson:"mode"`
	Enabled   *bool     `bson:"enabled"`
	InjectCSS string    `bson:"injectCss,omitempty"`
	CreatedAt time.Time `bson:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

// CustomPageView 是给前端/菜单用的视图对象(camelCase，与其余接口一致)。
type CustomPageView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Mode    string `json:"mode"`
	Enabled bool   `json:"enabled"`
	// Path 是平台内的访问路径(反代模式才有值)，仅供前端展示，让用户知道菜单指向哪。
	Path string `json:"path,omitempty"`
}

func (d customPageDoc) enabled() bool {
	// 旧文档无 enabled 字段 → 视为启用(与原 Flink 单例的兼容口径一致)
	if d.Enabled == nil {
		return true
	}
	return *d.Enabled
}

func (d customPageDoc) mode() string {
	if d.Mode == customPageModeIframe {
		return customPageModeIframe
	}
	return customPageModeProxy
}

func (d customPageDoc) view() CustomPageView {
	v := CustomPageView{
		ID:      d.ID,
		Name:    d.Name,
		URL:     strings.TrimSpace(d.URL),
		Mode:    d.mode(),
		Enabled: d.enabled(),
	}
	if v.Mode == customPageModeProxy {
		v.Path = "/custom/" + d.ID
	}
	return v
}

// listCustomPageDocs 按创建时间读全部自定义页面(菜单顺序 = 创建顺序，稳定可预期)。
func listCustomPageDocs(ctx context.Context) ([]customPageDoc, error) {
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindCustomPage},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []customPageDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func findCustomPageDoc(ctx context.Context, id string) (customPageDoc, bool) {
	var doc customPageDoc
	if err := database.Config().FindOne(ctx,
		bson.M{"_id": id, "kind": kindCustomPage}).Decode(&doc); err != nil {
		return customPageDoc{}, false
	}
	return doc, true
}

// ResolveCustomPage 供动态反代使用：返回该 slug 当前生效的目标地址与要注入的 CSS。
// 只有「启用 + 反代模式 + url 非空」才返回地址；否则空串(反代侧据此返回 503)。
func ResolveCustomPage(slug string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	doc, ok := findCustomPageDoc(ctx, slug)
	if !ok || !doc.enabled() || doc.mode() != customPageModeProxy {
		return "", ""
	}
	return strings.TrimSpace(doc.URL), doc.InjectCSS
}

// EnabledCustomPages 返回菜单要显示的自定义页面(已启用且 url 非空)。
// 供 /api/iframe-urls 出「自定义」分组。
func EnabledCustomPages() []CustomPageView {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	docs, err := listCustomPageDocs(ctx)
	if err != nil {
		return nil
	}
	var out []CustomPageView
	for _, d := range docs {
		if !d.enabled() || strings.TrimSpace(d.URL) == "" {
			continue
		}
		out = append(out, d.view())
	}
	return out
}

// slugify 把页面名转成能进 URL 的短标识：小写 ascii 字母数字，其余折成 "-"。
// 名称全是中文这类取不出 ascii 的，回退成 page-<随机 6 位>，保证一定有个稳定路径。
func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	if s == "" {
		buf := make([]byte, 3)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Sprintf("page-%d", time.Now().UnixNano()%1000000)
		}
		return "page-" + hex.EncodeToString(buf)
	}
	return s
}

// uniqueSlug 在已有 slug 上避让重名：langfuse → langfuse-2 → langfuse-3 …
func uniqueSlug(ctx context.Context, base string) string {
	slug := base
	for i := 2; i < 100; i++ {
		if _, exists := findCustomPageDoc(ctx, slug); !exists {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()%100000)
}

// customPageReq 是新建/更新的请求体。
type customPageReq struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Mode    string `json:"mode"`
	Enabled *bool  `json:"enabled"`
}

// normalize 校验并规整请求：返回 (name, url, mode, enabled, 错误 key)。
func (req customPageReq) normalize() (string, string, string, bool, string) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", "", "", false, "alert_custom_page_name_required"
	}
	u := strings.TrimSpace(req.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", "", false, "alert_custom_page_url_invalid"
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = customPageModeProxy
	}
	if mode != customPageModeProxy && mode != customPageModeIframe {
		return "", "", "", false, "alert_custom_page_mode_invalid"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return name, u, mode, enabled, ""
}

// probeCustomPage 保存前探一下地址，语义与其它数据源一致：只保证「用户知道」，不替用户否决。
// 连不通 ≠ 配置错(目标还没起、隔离网段暂时探不到都是合法场景)，所以照存不误，原因随响应带回。
func probeCustomPage(u string, enabled bool) string {
	if u == "" || !enabled {
		return ""
	}
	if err, _ := service.ProbeWebUICapabilities(u); err != nil {
		return err.Error()
	}
	return ""
}

// ListCustomPages GET /api/alert/custom-pages → [{id,name,url,mode,enabled,path}]
func ListCustomPages(w http.ResponseWriter, r *http.Request) {
	docs, err := listCustomPageDocs(r.Context())
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	out := make([]CustomPageView, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.view())
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateCustomPage POST /api/alert/custom-pages  body {name,url,mode?,enabled?}
func CreateCustomPage(w http.ResponseWriter, r *http.Request) {
	var req customPageReq
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	name, u, mode, enabled, errKey := req.normalize()
	if errKey != "" {
		httputil.WriteErr(w, http.StatusBadRequest, errKey)
		return
	}

	ctx := r.Context()
	id := uniqueSlug(ctx, slugify(name))
	now := time.Now()
	doc := customPageDoc{
		ID: id, Kind: kindCustomPage, Name: name, URL: u, Mode: mode,
		Enabled: &enabled, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := database.Config().InsertOne(ctx, doc); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	resp := map[string]any{"page": doc.view()}
	if probeErr := probeCustomPage(u, enabled); probeErr != "" {
		resp["probeError"] = probeErr
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// UpdateCustomPage PUT /api/alert/custom-pages/{id}  body {name,url,mode?,enabled?}
// slug(=_id，即访问路径)建后不改：菜单地址一旦发出去就该稳定，改名不该让链接失效。
func UpdateCustomPage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var req customPageReq
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	name, u, mode, enabled, errKey := req.normalize()
	if errKey != "" {
		httputil.WriteErr(w, http.StatusBadRequest, errKey)
		return
	}

	ctx := r.Context()
	old, ok := findCustomPageDoc(ctx, id)
	if !ok {
		httputil.WriteErr(w, http.StatusNotFound, "alert_custom_page_not_found")
		return
	}
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": id, "kind": kindCustomPage},
		bson.M{"$set": bson.M{
			"name": name, "url": u, "mode": mode,
			"enabled": enabled, "updatedAt": time.Now(),
		}}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	old.Name, old.URL, old.Mode, old.Enabled = name, u, mode, &enabled
	resp := map[string]any{"page": old.view()}
	if probeErr := probeCustomPage(u, enabled); probeErr != "" {
		resp["probeError"] = probeErr
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// DeleteCustomPage DELETE /api/alert/custom-pages/{id}
func DeleteCustomPage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	res, err := database.Config().DeleteOne(r.Context(), bson.M{"_id": id, "kind": kindCustomPage})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_custom_page_not_found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// MigrateFlinkConfig 把 1.5.11 及以前的 Flink 单例({_id:"flink", kind:"flinkConfig"})
// 转成一条自定义页面。
//
// ⚠️ 必须「就地改 kind」，不能「读旧的、写一条新的」：两者 _id 都是 flink，同一个集合
// 装不下两条，插新的直接撞 E11000 duplicate key（实测就是这么炸的：迁移每次启动都失败、
// 客户升级后 Flink 入口再也不出现）。就地转换顺带解决了幂等——转完这条已经是 customPage，
// 下次启动按 kind:"flinkConfig" 查不到；用户把它删掉后文档也没了，不会重启后自己长回来。
func MigrateFlinkConfig() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var old struct {
		URL     string `bson:"url"`
		Enabled *bool  `bson:"enabled"`
	}
	if err := database.Config().FindOne(ctx,
		bson.M{"_id": "flink", "kind": "flinkConfig"}).Decode(&old); err != nil {
		return // 没有旧文档 = 全新部署，或早已迁过
	}

	url := strings.TrimSpace(old.URL)
	if url == "" {
		// 旧文档在、但地址是空的（用户清除过 Flink 入口）——没有可迁的内容。
		// 删掉这个空壳，否则每次启动都要来查一遍。
		if _, err := database.Config().DeleteOne(ctx,
			bson.M{"_id": "flink", "kind": "flinkConfig"}); err != nil {
			fmt.Printf("[CustomPage] 清理空的 Flink 旧配置失败: %v\n", err)
		}
		return
	}

	enabled := true // 旧文档无 enabled 字段 → 视为启用，别让升级把已配好的入口关掉
	if old.Enabled != nil {
		enabled = *old.Enabled
	}
	now := time.Now()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": "flink", "kind": "flinkConfig"},
		bson.M{"$set": bson.M{
			"kind": kindCustomPage, "name": "Flink", "mode": customPageModeProxy,
			"url": url, "enabled": enabled, "injectCss": flinkInjectCSS,
			"createdAt": now, "updatedAt": now,
		}}); err != nil {
		fmt.Printf("[CustomPage] Flink 配置迁移失败(下次启动会重试): %v\n", err)
		return
	}
	fmt.Printf("[CustomPage] 已把 Flink 入口迁成自定义页面: %s(访问路径 /custom/flink)\n", url)
}
