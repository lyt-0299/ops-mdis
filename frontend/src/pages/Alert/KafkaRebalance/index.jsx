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
  createKafkaRebalanceMonitor,
  deleteKafkaRebalanceMonitor,
  getKafkaRebalanceMonitorAlertHistory,
  listKafkaInstances,
  listKafkaRebalanceMonitors,
  listWebhooks,
  pauseKafkaRebalanceMonitor,
  refreshKafkaRebalanceMonitor,
  resumeKafkaRebalanceMonitor,
  updateKafkaRebalanceMonitor,
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
  { value: '',                  label: _l('全部状态') },
  { value: 'active_OK',         label: 'OK' },
  { value: 'active_Pending',    label: _l('预警') },
  { value: 'active_Firing',     label: _l('告警') },
  { value: 'active_Recovered',  label: _l('恢复') },
  { value: 'paused',            label: _l('已暂停') },
];

// Last-state badge mapping (rebalance-specific states)
const LAST_STATE_TAG = {
  normal:      { color: 'success', text: _l('正常') },
  rebalancing: { color: 'warning', text: _l('重平衡中') },
  empty:       { color: 'default', text: _l('无活跃成员') },
  orphaned:    { color: 'default', text: _l('分区未分配') },
  error:       { color: 'error',   text: _l('检测失败') },
};

// ── Helpers ───────────────────────────────────────────────────────────────────

function formatDuration(secs) {
  if (!secs && secs !== 0) return '-';
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.round(secs / 60)}m`;
  return `${Math.round(secs / 3600)}h`;
}

function formatTime(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  } catch { return iso; }
}

// Convert consumer_groups array ↔ textarea string
const groupsToText = (groups) =>
  Array.isArray(groups) ? groups.join('\n') : (groups || '');

const textToGroups = (text) =>
  (text || '')
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);

const emptyForm = () => ({
  name:            '',
  kafkaInstanceId: undefined,
  consumerGroup:   '',
  topic:           '',
  interval:        60,
  alertNote:       '',
  webhookId:       undefined,
});

// ── Main component ────────────────────────────────────────────────────────────

const KafkaRebalance = () => {
  const [loading,          setLoading]          = useState(false);
  const [monitors,         setMonitors]         = useState([]);
  const [kafkaInstances,   setKafkaInstances]   = useState([]);
  const [webhooks,         setWebhooks]         = useState([]);

  // Filters
  const [filterState,    setFilterState]    = useState('');
  const [filterInstance, setFilterInstance] = useState('');
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

  const fetchMonitors = useCallback(() => {
    setLoading(true);
    listKafkaRebalanceMonitors()
      .then(({ data }) => setMonitors(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 Kafka 重平衡监控列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    setLoading(true);
    Promise.all([listKafkaRebalanceMonitors(), listKafkaInstances(), listWebhooks()])
      .then(([monRes, insRes, whRes]) => {
        setMonitors(Array.isArray(monRes.data) ? monRes.data : []);
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

  const filteredMonitors = useMemo(() => {
    return monitors.filter((r) => {
      if (filterState) {
        if (filterState === 'paused' && r.status !== 'paused') return false;
        if (filterState !== 'paused') {
          if (r.status === 'paused') return false;
          const stateType = filterState.replace('active_', '');
          if (r.alertState !== stateType) return false;
        }
      }
      if (filterInstance && r.kafkaInstanceId !== filterInstance) return false;
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        const group = r.consumerGroup || '';
        if (
          !r.name?.toLowerCase().includes(q) &&
          !group.toLowerCase().includes(q) &&
          !(r.topic || '').toLowerCase().includes(q)
        ) return false;
      }
      return true;
    });
  }, [monitors, filterState, filterInstance, filterSearch]);

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
    // consumerGroup is a single string in our backend model
    const groupText = record.consumerGroup || '';
    form.setFieldsValue({
      name:            record.name,
      kafkaInstanceId: record.kafkaInstanceId,
      consumerGroup:   groupText,
      topic:           record.topic || '',
      interval:        record.interval || 60,
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
      const groups = textToGroups(values.consumerGroup);
      if (!groups.length) {
        message.error(_l('请至少输入一个消费组'));
        setConfirmLoading(false);
        return;
      }
      const payload = {
        name:            values.name.trim(),
        kafkaInstanceId: values.kafkaInstanceId,
        consumerGroup:   groups[0],        // primary field (single)
        topic:           (values.topic || '').trim(),
        interval:        values.interval,
        alertNote:       values.alertNote || '',
        webhookId:       values.webhookId || null,
      };
      const req = editingRecord
        ? updateKafkaRebalanceMonitor(editingRecord.id, payload)
        : createKafkaRebalanceMonitor(payload);
      req
        .then(() => {
          message.success(editingRecord ? _l('更新成功') : _l('创建成功'));
          closeModal();
          fetchMonitors();
        })
        .catch((e) => message.error(apiErr(e, editingRecord ? '更新失败，请重试' : '创建失败，请重试')))
        .finally(() => setConfirmLoading(false));
    }).catch(() => setConfirmLoading(false));
  };

  const handleDelete = (record) => {
    deleteKafkaRebalanceMonitor(record.id)
      .then(() => { message.success(_l('删除成功')); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  // ── Refresh ───────────────────────────────────────────────────────────────

  const handleRefresh = (record) => {
    setRefreshingId(record.id);
    refreshKafkaRebalanceMonitor(record.id)
      .then(() => { message.success(_l('已触发立即检查')); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('立即检查失败'))))
      .finally(() => setRefreshingId(null));
  };

  // ── Pause / Resume ────────────────────────────────────────────────────────

  const openPause    = (record) => { setPauseRecord(record); setPauseOpen(true); };
  const handleResume = (record) => {
    resumeKafkaRebalanceMonitor(record.id)
      .then(() => { message.success(_l('已启用')); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('启用失败，请重试'))));
  };

  const handlePauseConfirm = (payload) => {
    if (!pauseRecord) return;
    setPauseLoading(true);
    pauseKafkaRebalanceMonitor(pauseRecord.id, payload)
      .then(() => { message.success(_l('已暂停')); setPauseOpen(false); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('暂停失败，请重试'))))
      .finally(() => setPauseLoading(false));
  };

  // ── History ───────────────────────────────────────────────────────────────

  const openHistory  = (record) => { setHistoryRecord(record); setHistoryOpen(true); };
  const fetchHistory = useCallback(
    (page, pageSize) => {
      if (!historyRecord) return Promise.resolve({ data: { items: [], total: 0 } });
      return getKafkaRebalanceMonitorAlertHistory(historyRecord.id, page, pageSize);
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
        title: _l('消费组'),
        dataIndex: 'consumerGroup',
        ellipsis: true,
        render: (v) => (
          <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v || '-'}</span>
        ),
      },
      {
        title: 'Topic',
        dataIndex: 'topic',
        ellipsis: true,
        render: (v) => v
          ? <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</span>
          : <span style={{ color: '#bbbbbb' }}>-</span>,
      },
      {
        title: _l('检查频率'),
        width: 100,
        align: 'center',
        render: (_, r) => formatDuration(r.interval),
      },
      {
        title: _l('检查结果'),
        width: 130,
        render: (_, r) => {
          const state = r.lastState;
          if (!state) return <span style={{ color: '#bbbbbb' }}>-</span>;
          const cfg = LAST_STATE_TAG[state] || { color: 'default', text: state };
          return <Tag color={cfg.color}>{cfg.text}</Tag>;
        },
      },
      {
        title: _l('最近检查'),
        width: 145,
        render: (_, r) => (
          <span style={{ fontFamily: 'monospace', fontSize: 11 }}>
            {formatTime(r.lastCheckedAt)}
          </span>
        ),
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
    <div className="kafkaRebalanceContainer">
      {/* Toolbar */}
      <div className="krToolbar">
        <div className="krFilters">
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

          <Input.Search
            placeholder={_l('搜索任务 / 消费组 / Topic')}
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
      <div className="krTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={filteredMonitors}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false }}
          tableLayout="auto"
          scroll={{ x: 'max-content' }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('暂无 Kafka 重平衡监控，点击「新建任务」添加')}
              />
            ),
          }}
        />
      </div>

      {/* Create / Edit Modal */}
      <Modal
        title={editingRecord ? _l('编辑 Kafka 重平衡检查') : _l('新建 Kafka 重平衡检查')}
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
            <Input placeholder={_l('例如：工作流消费组重平衡监控')} />
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
            name="consumerGroup"
            label={_l('消费组')}
            rules={[{ required: true, message: _l('请输入消费组') }]}
            extra={_l('消费组 ID')}
          >
            <TextArea
              rows={3}
              placeholder={'md-workflow-consumer\nmd-hook-consumer'}
              style={{ fontFamily: 'monospace', fontSize: 12 }}
            />
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
            name="interval"
            label={_l('检查频率（秒）')}
            rules={[{ required: true, message: _l('请输入检查频率') }]}
          >
            <InputNumber min={30} addonAfter="s" style={{ width: 180 }} placeholder="60" />
          </Form.Item>

          <Form.Item name="webhookId" label={_l('通知渠道')}>
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

export default KafkaRebalance;
