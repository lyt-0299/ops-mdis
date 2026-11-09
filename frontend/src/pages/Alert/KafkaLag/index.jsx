import React, { useCallback,useEffect, useMemo, useState } from 'react';
import { PlusOutlined } from '@ant-design/icons';
import {
  Button,
  Empty,
  Form,
  Input,
  InputNumber,
  message,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import {
  createTask,
  deleteTask,
  getTaskAlertHistory,
  listKafkaInstances,
  listTasks,
  listWebhooks,
  pauseTask,
  refreshTask,
  resumeTask,
  updateTask,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

import AlertHistoryModal from '../components/AlertHistoryModal';
import PauseModal        from '../components/PauseModal';

import './index.less';

const { Option } = Select;
const { TextArea } = Input;

// ── Alert state tags ──────────────────────────────────────────────────────────

const ALERT_STATE_TAG = {
  OK:        { color: 'success',    text: 'OK' },
  Pending:   { color: 'warning',    text: _l('预警') },
  Firing:    { color: 'error',      text: _l('告警') },
  Recovered: { color: 'processing', text: _l('恢复') },
};

const STATE_FILTER_OPTIONS = [
  { value: '',               label: _l('全部状态') },
  { value: 'active_OK',      label: 'OK' },
  { value: 'active_Pending', label: _l('预警') },
  { value: 'active_Firing',  label: _l('告警') },
  { value: 'active_Recovered', label: _l('恢复') },
  { value: 'paused',         label: _l('已暂停') },
];

// ── Helpers ───────────────────────────────────────────────────────────────────

function formatDuration(secs) {
  if (!secs && secs !== 0) return '-';
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.round(secs / 60)}m`;
  return `${Math.round(secs / 3600)}h`;
}

function formatNumber(n) {
  if (n == null) return '-';
  return Number(n).toLocaleString();
}

function formatTime(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  } catch { return iso; }
}

const emptyForm = () => ({
  name:            '',
  kafkaInstanceId: undefined,
  topic:           '',
  consumerGroup:   '',
  threshold:       10000,
  interval:        180,
  alertNote:       '',
  webhookId:       undefined,
});

// ── Main component ────────────────────────────────────────────────────────────

const KafkaLag = () => {
  const [loading,          setLoading]          = useState(false);
  const [tasks,            setTasks]            = useState([]);
  const [kafkaInstances,   setKafkaInstances]   = useState([]);
  const [webhooks,         setWebhooks]         = useState([]);

  // Filters
  const [filterState,    setFilterState]    = useState('');
  const [filterInstance, setFilterInstance] = useState('');
  const [filterWebhook,  setFilterWebhook]  = useState('');
  const [filterSearch,   setFilterSearch]   = useState('');

  // CRUD modal
  const [modalVisible,   setModalVisible]   = useState(false);
  const [editingRecord,  setEditingRecord]  = useState(null);
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();

  // Refreshing row
  const [refreshingId, setRefreshingId] = useState(null);

  // History modal
  const [historyOpen,   setHistoryOpen]   = useState(false);
  const [historyRecord, setHistoryRecord] = useState(null);

  // Pause modal
  const [pauseOpen,    setPauseOpen]    = useState(false);
  const [pauseRecord,  setPauseRecord]  = useState(null);
  const [pauseLoading, setPauseLoading] = useState(false);

  // ── Data fetching ─────────────────────────────────────────────────────────

  const fetchTasks = useCallback(() => {
    setLoading(true);
    listTasks()
      .then(({ data }) => setTasks(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 Kafka 堆积监控列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    setLoading(true);
    Promise.all([listTasks(), listKafkaInstances(), listWebhooks()])
      .then(([taskRes, insRes, whRes]) => {
        setTasks(Array.isArray(taskRes.data) ? taskRes.data : []);
        setKafkaInstances(Array.isArray(insRes.data) ? insRes.data : []);
        setWebhooks(Array.isArray(whRes.data) ? whRes.data : []);
      })
      .catch((e) => message.error(apiErr(e, _l('数据加载失败'))))
      .finally(() => setLoading(false));
  }, []);

  const getInstanceName = (id) =>
    kafkaInstances.find((k) => k.id === id)?.name || '-';
  const getWebhookName  = (id) =>
    webhooks.find((w) => w.id === id)?.name || '-';

  // ── Filtered data ─────────────────────────────────────────────────────────

  const filteredTasks = useMemo(() => {
    return tasks.filter((r) => {
      if (filterState) {
        if (filterState === 'paused' && r.status !== 'paused') return false;
        if (filterState !== 'paused') {
          if (r.status === 'paused') return false;
          const stateType = filterState.replace('active_', '');
          if (r.alertState !== stateType) return false;
        }
      }
      if (filterInstance && r.kafkaInstanceId !== filterInstance) return false;
      if (filterWebhook) {
        if (filterWebhook === 'none' && r.webhookId != null) return false;
        if (filterWebhook !== 'none' && r.webhookId !== filterWebhook) return false;
      }
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        if (
          !r.name?.toLowerCase().includes(q) &&
          !r.topic?.toLowerCase().includes(q) &&
          !r.consumerGroup?.toLowerCase().includes(q)
        ) return false;
      }
      return true;
    });
  }, [tasks, filterState, filterInstance, filterWebhook, filterSearch]);

  // ── CRUD modal ────────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingRecord(null);
    form.resetFields();
    form.setFieldsValue(emptyForm());
    setModalVisible(true);
  };

  const openEdit = (record) => {
    setEditingRecord(record);
    form.resetFields();
    form.setFieldsValue({
      name:            record.name,
      kafkaInstanceId: record.kafkaInstanceId,
      topic:           record.topic || '',
      consumerGroup:   record.consumerGroup || '',
      threshold:       record.threshold ?? 10000,
      interval:        record.interval || 180,
      alertNote:       record.alertNote || '',
      webhookId:       record.webhookId,
    });
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setEditingRecord(null);
    form.resetFields();
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);
      const payload = { ...values };
      const req = editingRecord
        ? updateTask(editingRecord.id, payload)
        : createTask(payload);
      req
        .then(() => {
          message.success(editingRecord ? _l('更新成功') : _l('创建成功'));
          closeModal();
          fetchTasks();
        })
        .catch((e) => message.error(apiErr(e, editingRecord ? '更新失败，请重试' : '创建失败，请重试')))
        .finally(() => setConfirmLoading(false));
    }).catch(() => setConfirmLoading(false));
  };

  const handleDelete = (record) => {
    deleteTask(record.id)
      .then(() => { message.success(_l('删除成功')); fetchTasks(); })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  // ── Refresh ───────────────────────────────────────────────────────────────

  const handleRefresh = (record) => {
    setRefreshingId(record.id);
    refreshTask(record.id)
      .then(() => { message.success(_l('已触发立即检查')); fetchTasks(); })
      .catch((e) => message.error(apiErr(e, _l('立即检查失败'))))
      .finally(() => setRefreshingId(null));
  };

  // ── Pause / Resume ────────────────────────────────────────────────────────

  const openPause    = (record) => { setPauseRecord(record); setPauseOpen(true); };
  const handleResume = (record) => {
    resumeTask(record.id)
      .then(() => { message.success(_l('已启用')); fetchTasks(); })
      .catch((e) => message.error(apiErr(e, _l('启用失败，请重试'))));
  };

  const handlePauseConfirm = (payload) => {
    if (!pauseRecord) return;
    setPauseLoading(true);
    pauseTask(pauseRecord.id, payload)
      .then(() => { message.success(_l('已暂停')); setPauseOpen(false); fetchTasks(); })
      .catch((e) => message.error(apiErr(e, _l('暂停失败，请重试'))))
      .finally(() => setPauseLoading(false));
  };

  // ── History ───────────────────────────────────────────────────────────────

  const openHistory  = (record) => { setHistoryRecord(record); setHistoryOpen(true); };
  const fetchHistory = useCallback(
    (page, pageSize) => {
      if (!historyRecord) return Promise.resolve({ data: { items: [], total: 0 } });
      return getTaskAlertHistory(historyRecord.id, page, pageSize);
    },
    [historyRecord],
  );

  // ── Columns ───────────────────────────────────────────────────────────────

  const columns = useMemo(
    () => [
      {
        title: _l('状态'),
        width: 110,
        render: (_, r) => {
          if (r.status === 'paused') return <Tag color="default">{_l('已暂停')}</Tag>;
          const cfg = ALERT_STATE_TAG[r.alertState] || { color: 'default', text: r.alertState };
          return <Tag color={cfg.color}>{cfg.text}</Tag>;
        },
      },
      { title: _l('任务名'), dataIndex: 'name', ellipsis: true },
      {
        title: _l('Kafka 实例'),
        ellipsis: true,
        render: (_, r) => <Tag>{getInstanceName(r.kafkaInstanceId)}</Tag>,
      },
      {
        title: 'Topic',
        dataIndex: 'topic',
        ellipsis: true,
        // 留空 = 整个消费组，显示成 '-' 会被当成「没配」
        render: (v) => (v
          ? <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</span>
          : <span style={{ fontSize: 12, color: '#999' }}>{_l('整个消费组')}</span>),
      },
      {
        title: _l('消费组'),
        dataIndex: 'consumerGroup',
        ellipsis: true,
        render: (v) => <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v || '-'}</span>,
      },
      {
        title: _l('Lag 阈值'),
        width: 100,
        align: 'right',
        render: (_, r) => (
          <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
            {formatNumber(r.threshold)}
          </span>
        ),
      },
      {
        title: _l('检查频率'),
        width: 100,
        align: 'center',
        render: (_, r) => formatDuration(r.interval),
      },
      {
        title: _l('当前 Lag'),
        width: 110,
        align: 'right',
        render: (_, r) => {
          const val = r.currentLag ?? r.lastLag;
          if (val == null) return <span style={{ color: '#bbbbbb' }}>-</span>;
          const isFiring = r.alertState === 'Firing';
          return (
            <span
              style={{
                fontFamily: 'monospace',
                fontSize: 12,
                fontWeight: isFiring ? 600 : 400,
                color: isFiring ? '#f5222d' : undefined,
              }}
            >
              {formatNumber(val)}
            </span>
          );
        },
      },
      {
        title: _l('通知渠道'),
        width: 130,
        ellipsis: true,
        render: (_, r) =>
          r.webhookId
            ? <Tag>{getWebhookName(r.webhookId)}</Tag>
            : <span style={{ color: '#bbbbbb' }}>-</span>,
      },
      {
        title: _l('操作'),
        width: 170,
        fixed: 'right',
        render: (_, record) => (
          <span className="opsActionGroup">
            <ActionIcon
              type="refresh"
              title={_l('立即检查')}
              onClick={() => handleRefresh(record)}
              loading={refreshingId === record.id}
            />
            {record.status === 'paused' ? (
              <ActionIcon type="resume" title={_l('启用')} onClick={() => handleResume(record)} />
            ) : (
              <ActionIcon type="pause" title={_l('暂停')} onClick={() => openPause(record)} />
            )}
            <ActionIcon type="edit" title={_l('编辑')} onClick={() => openEdit(record)} />
            <ActionIcon type="history" title={_l('告警历史')} onClick={() => openHistory(record)} />
            <Popconfirm
              title={_l('确认删除')}
              description={_l('删除后不可恢复，确认继续？')}
              okText={_l('删除')}
              okType="danger"
              cancelText={_l('取消')}
              onConfirm={() => handleDelete(record)}
            >
              <span>
                <ActionIcon type="delete" title={_l('删除')} danger />
              </span>
            </Popconfirm>
          </span>
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [kafkaInstances, webhooks, refreshingId],
  );

  // ── Render ────────────────────────────────────────────────────────────────

  return (
    <div className="kafkaLagContainer">
      {/* Toolbar */}
      <div className="klToolbar">
        <div className="klFilters">
          <Select
            value={filterState}
            style={{ width: 120 }}
            onChange={setFilterState}
          >
            {STATE_FILTER_OPTIONS.map((o) => (
              <Option key={o.value} value={o.value}>{o.label}</Option>
            ))}
          </Select>

          <Select
            allowClear
            placeholder={_l('Kafka 实例')}
            style={{ width: 160 }}
            value={filterInstance || undefined}
            onChange={(v) => setFilterInstance(v || '')}
          >
            {kafkaInstances.map((k) => (
              <Option key={k.id} value={k.id}>{k.name}</Option>
            ))}
          </Select>

          <Select
            allowClear
            placeholder={_l('通知渠道')}
            style={{ width: 190 }}
            value={filterWebhook || undefined}
            onChange={(v) => setFilterWebhook(v || '')}
          >
            <Option value="none">{_l('无通知渠道')}</Option>
            {webhooks.map((w) => (
              <Option key={w.id} value={w.id}>{w.name}</Option>
            ))}
          </Select>

          <Input.Search
            placeholder={_l('搜索任务 / Topic / 消费组')}
            value={filterSearch}
            onChange={(e) => setFilterSearch(e.target.value)}
            onSearch={(v) => setFilterSearch(v)}
            style={{ width: 220 }}
            allowClear
          />

          <Button
            onClick={() => {
              setFilterState('');
              setFilterInstance('');
              setFilterWebhook('');
              setFilterSearch('');
            }}
          >
            {_l('重置')}
          </Button>
        </div>

        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          {_l('新建任务')}
        </Button>
      </div>

      {/* Table */}
      <div className="klTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={filteredTasks}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false }}
          tableLayout="auto"
          scroll={{ x: 'max-content' }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('暂无 Kafka 堆积监控，点击「新建任务」添加')}
              />
            ),
          }}
        />
      </div>

      {/* Create / Edit Modal */}
      <Modal
        title={editingRecord ? _l('编辑 Kafka 堆积监控') : _l('新建 Kafka 堆积监控')}
        open={modalVisible}
        width={560}
        maskClosable={false}
        confirmLoading={confirmLoading}
        okText={_l('保存')}
        cancelText={_l('取消')}
        onOk={handleSave}
        onCancel={closeModal}
        destroyOnClose
      >
        <Form form={form} layout="vertical" style={{ marginTop: 8 }}>
          <Form.Item
            name="name"
            label={_l('任务名称')}
            rules={[{ required: true, message: _l('请输入任务名称') }]}
          >
            <Input placeholder={_l('例如：核心业务消费组堆积监控')} />
          </Form.Item>

          <Form.Item
            name="kafkaInstanceId"
            label={_l('Kafka 实例')}
            rules={[{ required: true, message: _l('请选择 Kafka 实例') }]}
          >
            <Select
              placeholder={_l('选择 Kafka 实例')}
              notFoundContent={_l('请先在「数据源」页添加对应数据源')}
            >
              {kafkaInstances.map((k) => (
                <Option key={k.id} value={k.id}>{k.name}</Option>
              ))}
            </Select>
          </Form.Item>

          <Form.Item
            name="topic"
            label={
              <span>
                Topic
                <span style={{ marginLeft: 6, fontSize: 12, color: '#999' }}>
                  ({_l('可选，留空则监控整个消费组')})
                </span>
              </span>
            }
          >
            <Input
              placeholder={_l('留空则监控整个消费组的所有 Topic')}
              style={{ fontFamily: 'monospace' }}
            />
          </Form.Item>

          <Form.Item
            name="consumerGroup"
            label={_l('消费组')}
            rules={[{ required: true, message: _l('请输入消费组') }]}
          >
            <Input
              placeholder={_l('例如：order-consumer-group')}
              style={{ fontFamily: 'monospace' }}
            />
          </Form.Item>

          <Form.Item label={_l('Lag 阈值 / 检查频率（秒）')} style={{ marginBottom: 0 }}>
            <Space>
              <Form.Item
                name="threshold"
                noStyle
                rules={[{ required: true, message: _l('请输入 Lag 阈值') }]}
              >
                <InputNumber
                  min={0}
                  style={{ width: 160 }}
                  addonBefore="Lag ≥"
                  placeholder="10000"
                />
              </Form.Item>
              <Form.Item name="interval" noStyle>
                <InputNumber min={10} addonAfter="s" style={{ width: 140 }} placeholder="180" />
              </Form.Item>
            </Space>
          </Form.Item>

          <Form.Item name="webhookId" label={_l('通知渠道')} style={{ marginTop: 16 }}>
            <Select allowClear placeholder={_l('选择通知渠道（可选）')}>
              {webhooks.map((w) => (
                <Option key={w.id} value={w.id}>{w.name}</Option>
              ))}
            </Select>
          </Form.Item>

          <Form.Item name="alertNote" label={_l('告警备注')}>
            <TextArea rows={2} placeholder={_l('告警通知附带的说明文字（可选）')} />
          </Form.Item>
        </Form>
      </Modal>

      {/* Alert history modal */}
      <AlertHistoryModal
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        title={historyRecord?.name}
        fetchHistory={fetchHistory}
      />

      {/* Pause modal */}
      <PauseModal
        open={pauseOpen}
        onCancel={() => setPauseOpen(false)}
        onConfirm={handlePauseConfirm}
        count={1}
        loading={pauseLoading}
      />
    </div>
  );
};

export default KafkaLag;
