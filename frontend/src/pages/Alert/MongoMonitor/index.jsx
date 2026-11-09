/**
 * MongoMonitor — MongoDB 监控页
 *
 * monitorType 枚举（camelCase 字段名 monitorType，与 model 一致）：
 *   replica_status  副本集节点状态
 *   oplog_lag       Oplog 延迟（秒）
 *   oplog_window    Oplog 保存时间（小时）
 *
 * 所有 JSON key 均为 camelCase，与 Go struct json tag 保持一致。
 */
import React, { useCallback, useEffect, useMemo, useState } from 'react';
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
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import {
  cancelMongoMonitorSilence,
  createMongoMonitor,
  createMongoMonitorSilences,
  deleteMongoMonitor,
  getMongoMonitorAlertHistory,
  listMongoInstances,
  listMongoMonitorInstances,
  listMongoMonitors,
  listWebhooks,
  pauseMongoMonitor,
  refreshMongoMonitor,
  resumeMongoMonitor,
  updateMongoMonitor,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

import AlertHistoryModal from '../components/AlertHistoryModal';
import InstancesSilenceModal from '../components/InstancesSilenceModal';
import PauseModal from '../components/PauseModal';

import './index.less';

const { Option } = Select;
const { TextArea } = Input;

// ── Constants ─────────────────────────────────────────────────────────────────

const MONITOR_TYPE_LABELS = {
  replica_status: _l('副本集节点状态'),
  oplog_lag: 'Oplog ' + _l('延迟'),
  oplog_window: 'Oplog ' + _l('保存时间'),
};

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

const TYPE_FILTER_OPTIONS = [
  { value: '', label: _l('全部类型') },
  ...Object.entries(MONITOR_TYPE_LABELS).map(([k, v]) => ({ value: k, label: v })),
];

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

/**
 * Render last detection value cell.
 * lastDetail is a JSON string whose structure depends on monitorType.
 */
function renderLastDetail(rule) {
  const isPaused = rule.status === 'paused';
  const dimColor = isPaused ? '#bbbbbb' : '#555';

  if (rule.monitorType === 'replica_status') {
    try {
      if (!rule.lastDetail) return <span style={{ color: '#bbbbbb' }}>-</span>;
      const detail = JSON.parse(rule.lastDetail);
      if (detail.members && Array.isArray(detail.members)) {
        return (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {detail.members.map((m, idx) => (
              <div
                key={idx}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '3px 8px',
                  borderRadius: 6,
                  border: '1px solid #f0f0f0',
                  background: isPaused ? '#fafafa' : '#f8faff',
                  fontSize: 12,
                  minWidth: 200,
                }}
              >
                <span style={{ fontFamily: 'monospace', color: dimColor, marginRight: 8, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }} title={m.name}>
                  {m.name}
                </span>
                <Tag
                  color={isPaused ? 'default' : m.state_str === 'PRIMARY' ? 'blue' : m.state_str === 'SECONDARY' ? 'green' : 'default'}
                  style={{ fontSize: 11, margin: 0 }}
                >
                  {m.state_str}
                </Tag>
              </div>
            ))}
          </div>
        );
      }
    } catch { return <span style={{ color: '#bbbbbb' }}>-</span>; }
  }

  if (rule.monitorType === 'oplog_lag') {
    try {
      if (!rule.lastDetail) return <span style={{ color: '#bbbbbb' }}>-</span>;
      const detail = JSON.parse(rule.lastDetail);
      if (Array.isArray(detail)) {
        if (detail.length === 0) return <span style={{ color: '#bbbbbb', fontSize: 12 }}>{_l('无 Secondary 节点')}</span>;
        return (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {detail.map((m, idx) => (
              <div
                key={idx}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '3px 8px',
                  borderRadius: 6,
                  border: '1px solid #f0f0f0',
                  background: isPaused ? '#fafafa' : '#fffef0',
                  fontSize: 12,
                  minWidth: 200,
                }}
              >
                <span style={{ fontFamily: 'monospace', color: dimColor, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }} title={m.member_name}>
                  {m.member_name}
                  <span style={{ color: '#bbbbbb', fontSize: 11, marginLeft: 4 }}>({m.state_str})</span>
                </span>
                <span style={{ fontFamily: 'monospace', fontWeight: 600, color: isPaused ? '#bbbbbb' : '#d97706' }}>
                  {(m.lag_seconds ?? 0).toFixed(1)}s
                </span>
              </div>
            ))}
          </div>
        );
      }
    } catch { return <span style={{ color: '#bbbbbb' }}>-</span>; }
  }

  if (rule.monitorType === 'oplog_window') {
    try {
      if (!rule.lastDetail) return <span style={{ color: '#bbbbbb' }}>-</span>;
      const detail = JSON.parse(rule.lastDetail);
      if (Array.isArray(detail)) {
        return (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {detail.map((m, idx) => (
              <div
                key={idx}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '3px 8px',
                  borderRadius: 6,
                  border: '1px solid #f0f0f0',
                  background: isPaused ? '#fafafa' : '#f0fdf4',
                  fontSize: 12,
                  minWidth: 200,
                }}
              >
                <span style={{ fontFamily: 'monospace', color: dimColor, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }} title={m.member_name}>
                  {m.member_name}
                  <span style={{ color: '#bbbbbb', fontSize: 11, marginLeft: 4 }}>({m.state_str})</span>
                </span>
                <span style={{ fontFamily: 'monospace', fontWeight: 600, color: isPaused ? '#bbbbbb' : '#059669' }}>
                  {(m.window_hours ?? 0).toFixed(1)}h
                </span>
              </div>
            ))}
          </div>
        );
      }
    } catch { /* fall through */ }
  }

  // Fallback numeric
  if (rule.lastValue != null) {
    return (
      <span style={{ fontFamily: 'monospace', fontSize: 12, color: dimColor }}>
        {Number(rule.lastValue).toFixed(2)}
      </span>
    );
  }
  return <span style={{ color: '#bbbbbb' }}>-</span>;
}

/** Format instance lastValue for the InstancesSilenceModal formatValue prop. */
function formatInstanceValue(monitorType, inst) {
  if (monitorType === 'replica_status') return inst.lastValue >= 1 ? _l('正常') : _l('异常');
  if (monitorType === 'oplog_lag') return `${Number(inst.lastValue).toFixed(1)}s`;
  if (monitorType === 'oplog_window') return `${Number(inst.lastValue).toFixed(1)}h`;
  return Number(inst.lastValue).toFixed(2);
}

/** Default form values when creating a new monitor rule. */
const emptyForm = (monitorType = 'replica_status') => ({
  name: '',
  mongoInstanceId: undefined,
  monitorType,
  threshold: monitorType === 'oplog_lag' ? 300 : monitorType === 'oplog_window' ? 12 : 0,
  operator: monitorType === 'oplog_window' ? '<' : '>',
  duration: 120,
  recoveryDuration: 60,
  interval: monitorType === 'oplog_window' ? 1800 : 60,
  alertNote: '',
  remark: '',
  webhookId: undefined,
  repeatAlert: false,
  remindInterval: 21600,
});

// ── Main component ────────────────────────────────────────────────────────────

const MongoMonitor = () => {
  const [loading, setLoading] = useState(false);
  const [rules, setRules] = useState([]);
  const [mongoInstances, setMongoInstances] = useState([]);
  const [webhooks, setWebhooks] = useState([]);

  // Filters
  const [filterState, setFilterState] = useState('');
  const [filterType, setFilterType] = useState('');
  const [filterSearch, setFilterSearch] = useState('');

  // CRUD modal
  const [modalVisible, setModalVisible] = useState(false);
  const [editingRecord, setEditingRecord] = useState(null);
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();
  const watchedType = Form.useWatch('monitorType', form);

  // Refreshing row
  const [refreshingId, setRefreshingId] = useState(null);

  // History modal
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyRecord, setHistoryRecord] = useState(null);

  // Pause modal
  const [pauseOpen, setPauseOpen] = useState(false);
  const [pauseRecord, setPauseRecord] = useState(null);
  const [pauseLoading, setPauseLoading] = useState(false);

  // Instances silence modal
  const [instancesOpen, setInstancesOpen] = useState(false);
  const [instancesRecord, setInstancesRecord] = useState(null);
  const [instancesList, setInstancesList] = useState([]);
  const [instancesLoading, setInstancesLoading] = useState(false);
  const [instancesRefreshing, setInstancesRefreshing] = useState(false);

  // ── Data fetching ───────────────────────────────────────────────────────────

  const fetchRules = useCallback(() => {
    setLoading(true);
    listMongoMonitors()
      .then(({ data }) => setRules(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 MongoDB 监控列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    setLoading(true);
    Promise.all([listMongoMonitors(), listMongoInstances(), listWebhooks()])
      .then(([rulesRes, instRes, whRes]) => {
        setRules(Array.isArray(rulesRes.data) ? rulesRes.data : []);
        setMongoInstances(Array.isArray(instRes.data) ? instRes.data : []);
        setWebhooks(Array.isArray(whRes.data) ? whRes.data : []);
      })
      .catch((e) => message.error(apiErr(e, _l('数据加载失败'))))
      .finally(() => setLoading(false));
  }, []);

  const getInstanceName = (id) =>
    mongoInstances.find((i) => i.id === id)?.name || '-';
  const getWebhookName = (id) =>
    webhooks.find((w) => w.id === id)?.name || '-';

  // ── Filtered data ───────────────────────────────────────────────────────────

  const filteredRules = useMemo(() => {
    return rules.filter((r) => {
      if (filterState) {
        if (filterState === 'paused' && r.status !== 'paused') return false;
        if (filterState !== 'paused') {
          if (r.status === 'paused') return false;
          const stateType = filterState.replace('active_', '');
          if (r.alertState !== stateType) return false;
        }
      }
      if (filterType && r.monitorType !== filterType) return false;
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        const instName = getInstanceName(r.mongoInstanceId).toLowerCase();
        if (!r.name?.toLowerCase().includes(q) && !instName.includes(q)) return false;
      }
      return true;
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rules, filterState, filterType, filterSearch, mongoInstances]);

  // ── CRUD modal ──────────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingRecord(null);
    form.resetFields();
    form.setFieldsValue(emptyForm('replica_status'));
    setModalVisible(true);
  };

  const openEdit = (record) => {
    setEditingRecord(record);
    form.resetFields();
    form.setFieldsValue({
      name: record.name,
      mongoInstanceId: record.mongoInstanceId,
      monitorType: record.monitorType || 'replica_status',
      threshold: record.threshold ?? 0,
      operator: record.operator || '>',
      duration: record.duration || 120,
      recoveryDuration: record.recoveryDuration || 60,
      interval: record.interval || 60,
      alertNote: record.alertNote || '',
      remark: record.remark || '',
      webhookId: record.webhookId || undefined,
      repeatAlert: record.repeatAlert || false,
      remindInterval: record.remindInterval || 21600,
    });
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setEditingRecord(null);
    form.resetFields();
  };

  const handleMonitorTypeChange = (type) => {
    if (type === 'replica_status') {
      form.setFieldsValue({ threshold: 0, operator: '>', interval: 60, duration: 120, recoveryDuration: 60 });
    } else if (type === 'oplog_lag') {
      form.setFieldsValue({ threshold: 300, operator: '>', interval: 60, duration: 120, recoveryDuration: 60 });
    } else if (type === 'oplog_window') {
      form.setFieldsValue({ threshold: 12, operator: '<', interval: 1800, duration: 600, recoveryDuration: 600 });
    }
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);
      const payload = {
        name: values.name,
        mongoInstanceId: values.mongoInstanceId,
        monitorType: values.monitorType,
        // replica_status does not use threshold
        threshold: values.monitorType === 'replica_status' ? 0 : values.threshold,
        operator: values.operator || '>',
        duration: values.duration,
        recoveryDuration: values.recoveryDuration,
        interval: values.interval,
        alertNote: values.alertNote || '',
        remark: values.remark || '',
        webhookId: values.webhookId || '',
        repeatAlert: values.repeatAlert || false,
        remindInterval: values.remindInterval || 0,
      };
      const req = editingRecord
        ? updateMongoMonitor(editingRecord.id, payload)
        : createMongoMonitor(payload);
      req
        .then(() => {
          message.success(editingRecord ? _l('更新成功') : _l('创建成功'));
          closeModal();
          fetchRules();
        })
        .catch((e) => message.error(apiErr(e, editingRecord ? '更新失败，请重试' : '创建失败，请重试')))
        .finally(() => setConfirmLoading(false));
    }).catch(() => setConfirmLoading(false));
  };

  const handleDelete = (record) => {
    deleteMongoMonitor(record.id)
      .then(() => { message.success(_l('删除成功')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  // ── Refresh ─────────────────────────────────────────────────────────────────

  const handleRefresh = (record) => {
    setRefreshingId(record.id);
    refreshMongoMonitor(record.id)
      .then(() => { message.success(_l('已触发立即检查')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('立即检查失败'))))
      .finally(() => setRefreshingId(null));
  };

  // ── Pause / Resume ───────────────────────────────────────────────────────────

  const openPause = (record) => { setPauseRecord(record); setPauseOpen(true); };
  const handleResume = (record) => {
    resumeMongoMonitor(record.id)
      .then(() => { message.success(_l('已启用')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('启用失败，请重试'))));
  };

  const handlePauseConfirm = (payload) => {
    if (!pauseRecord) return;
    setPauseLoading(true);
    pauseMongoMonitor(pauseRecord.id, payload)
      .then(() => { message.success(_l('已暂停')); setPauseOpen(false); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('暂停失败，请重试'))))
      .finally(() => setPauseLoading(false));
  };

  // ── History ──────────────────────────────────────────────────────────────────

  const openHistory = (record) => { setHistoryRecord(record); setHistoryOpen(true); };
  const fetchHistory = useCallback(
    (page, pageSize) => {
      if (!historyRecord) return Promise.resolve({ data: { items: [], total: 0 } });
      return getMongoMonitorAlertHistory(historyRecord.id, page, pageSize);
    },
    [historyRecord],
  );

  // ── Instances silence ────────────────────────────────────────────────────────

  const openInstances = (record) => {
    setInstancesRecord(record);
    setInstancesList([]);
    setInstancesLoading(true);
    setInstancesOpen(true);
    listMongoMonitorInstances(record.id)
      .then(({ data }) => setInstancesList(Array.isArray(data?.items) ? data.items : []))
      .catch((e) => message.error(apiErr(e, _l('加载实例明细失败'))))
      .finally(() => setInstancesLoading(false));
  };

  const refreshInstancesList = () => {
    if (!instancesRecord || instancesRefreshing) return;
    setInstancesRefreshing(true);
    refreshMongoMonitor(instancesRecord.id)
      .then(() => listMongoMonitorInstances(instancesRecord.id))
      .then(({ data }) => {
        setInstancesList(Array.isArray(data?.items) ? data.items : []);
        message.success(_l('实例明细已刷新'));
        fetchRules();
      })
      .catch((e) => message.error(apiErr(e, _l('刷新实例明细失败'))))
      .finally(() => setInstancesRefreshing(false));
  };

  const handleCreateSilence = async (payload) => {
    if (!instancesRecord) return false;
    try {
      await createMongoMonitorSilences(instancesRecord.id, payload);
      message.success(_l('已静默该实例'));
      const { data } = await listMongoMonitorInstances(instancesRecord.id);
      setInstancesList(Array.isArray(data?.items) ? data.items : []);
      fetchRules();
      return true;
    } catch {
      message.error(_l('静默失败'));
      return false;
    }
  };

  const handleCancelSilence = async (inst) => {
    if (!instancesRecord) return false;
    try {
      await cancelMongoMonitorSilence(instancesRecord.id, inst.scopeKey);
      message.success(_l('已取消静默'));
      const { data } = await listMongoMonitorInstances(instancesRecord.id);
      setInstancesList(Array.isArray(data?.items) ? data.items : []);
      fetchRules();
      return true;
    } catch {
      message.error(_l('取消静默失败'));
      return false;
    }
  };

  // ── Columns ──────────────────────────────────────────────────────────────────

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
        title: 'MongoDB ' + _l('实例'),
        ellipsis: true,
        render: (_, r) => (
          <Tag>{getInstanceName(r.mongoInstanceId)}</Tag>
        ),
      },
      {
        title: _l('监控类型'),
        width: 140,
        render: (_, r) => (
          <Tag>{MONITOR_TYPE_LABELS[r.monitorType] || r.monitorType}</Tag>
        ),
      },
      {
        title: _l('阈值'),
        width: 110,
        align: 'center',
        render: (_, r) => {
          if (r.monitorType === 'replica_status') return <span style={{ color: '#bbbbbb' }}>-</span>;
          const unit = r.monitorType === 'oplog_lag' ? 's' : 'h';
          return (
            <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
              {r.operator} {r.threshold}{unit}
            </span>
          );
        },
      },
      {
        title: _l('检测值'),
        width: 260,
        render: (_, r) => renderLastDetail(r),
      },
      {
        title: _l('检查频率'),
        width: 100,
        align: 'center',
        render: (_, r) => formatDuration(r.interval),
      },
      {
        title: _l('持续（告警/恢复）'),
        width: 150,
        align: 'center',
        render: (_, r) => (
          <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
            {r.duration}s / {r.recoveryDuration}s
          </span>
        ),
      },
      {
        title: _l('最近检查'),
        width: 140,
        render: (_, r) => (
          <span style={{ fontFamily: 'monospace', fontSize: 11, color: '#999999' }}>
            {formatTime(r.lastCheckAt || r.updatedAt)}
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
        width: 200,
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
            <ActionIcon type="detail" title={_l('实例静默')} onClick={() => openInstances(record)} />
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
    [mongoInstances, webhooks, refreshingId],
  );

  // ── Render ────────────────────────────────────────────────────────────────────

  const currentType = watchedType || (editingRecord?.monitorType) || 'replica_status';

  return (
    <div className="mongoMonitorContainer">
      {/* Toolbar */}
      <div className="mmToolbar">
        <div className="mmFilters">
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
            value={filterType}
            style={{ width: 150 }}
            onChange={setFilterType}
          >
            {TYPE_FILTER_OPTIONS.map((o) => (
              <Option key={o.value} value={o.value}>{o.label}</Option>
            ))}
          </Select>

          <Input.Search
            placeholder={_l('搜索任务 / 实例名')}
            value={filterSearch}
            onChange={(e) => setFilterSearch(e.target.value)}
            onSearch={(v) => setFilterSearch(v)}
            style={{ width: 260 }}
            allowClear
          />

          <Button
            onClick={() => {
              setFilterState('');
              setFilterType('');
              setFilterSearch('');
            }}
          >
            {_l('重置')}
          </Button>
        </div>

        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          {_l('新建监控')}
        </Button>
      </div>

      {/* Table */}
      <div className="mmTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={filteredRules}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false }}
          tableLayout="auto"
          scroll={{ x: 'max-content' }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('暂无 MongoDB 监控，点击「新建监控」添加')}
              />
            ),
          }}
        />
      </div>

      {/* Create / Edit Modal */}
      <Modal
        title={editingRecord ? _l('编辑 MongoDB 监控') : _l('新建 MongoDB 监控')}
        open={modalVisible}
        width={620}
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
            <Input placeholder={_l('例如：生产环境副本集状态监控')} />
          </Form.Item>

          <Form.Item
            name="mongoInstanceId"
            label={'MongoDB ' + _l('实例')}
            rules={[{ required: true, message: _l('请选择 MongoDB 实例') }]}
          >
            <Select
              placeholder={_l('选择 MongoDB 实例')}
              notFoundContent={_l('暂无可告警的 MongoDB 实例，请在「数据源」页为副本集实例勾选「告警」用途')}
            >
              {/* 仅列出勾了「告警」用途的实例(与数据源页能力门控一致;单节点勾不上 alert,自然排除) */}
              {mongoInstances
                .filter((i) => Array.isArray(i.purposes) && i.purposes.includes('alert'))
                .map((i) => (
                  <Option key={i.id} value={i.id}>{i.name}</Option>
                ))}
            </Select>
          </Form.Item>

          <Form.Item
            name="monitorType"
            label={_l('监控类型')}
            rules={[{ required: true, message: _l('请选择监控类型') }]}
          >
            <Select onChange={handleMonitorTypeChange}>
              {Object.entries(MONITOR_TYPE_LABELS).map(([k, v]) => (
                <Option key={k} value={k}>{v}</Option>
              ))}
            </Select>
          </Form.Item>

          {currentType !== 'replica_status' && (
            <Form.Item label={_l('操作符 / 阈值')} style={{ marginBottom: 0 }}>
              <Space>
                <Form.Item name="operator" noStyle>
                  <Select style={{ width: 80 }}>
                    {['>', '<', '>=', '<='].map((o) => (
                      <Option key={o} value={o}>{o}</Option>
                    ))}
                  </Select>
                </Form.Item>
                <Form.Item
                  name="threshold"
                  noStyle
                  rules={[{ required: true, message: _l('请输入阈值') }]}
                >
                  <InputNumber
                    min={0}
                    style={{ width: 160 }}
                    addonAfter={currentType === 'oplog_lag' ? 's' : 'h'}
                    placeholder={currentType === 'oplog_lag' ? '300' : '12'}
                  />
                </Form.Item>
              </Space>
            </Form.Item>
          )}

          <Form.Item label={_l('检查频率 / 告警持续 / 恢复持续（秒）')} style={{ marginBottom: 0, marginTop: 16 }}>
            <Space wrap>
              <Form.Item name="interval" noStyle>
                <InputNumber min={10} addonAfter="s" style={{ width: 120 }} placeholder="60" />
              </Form.Item>
              <Form.Item name="duration" noStyle>
                <InputNumber min={0} addonAfter="s" style={{ width: 120 }} placeholder="120" />
              </Form.Item>
              <Form.Item name="recoveryDuration" noStyle>
                <InputNumber min={0} addonAfter="s" style={{ width: 120 }} placeholder="60" />
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

          <Form.Item name="remark" label={_l('任务备注')}>
            <TextArea rows={2} placeholder={_l('内部备注，不随告警发送（可选）')} />
          </Form.Item>

          <Form.Item label={_l('持续告警')} style={{ marginBottom: 0 }}>
            <Space align="center">
              <Form.Item name="repeatAlert" valuePropName="checked" noStyle>
                <Switch />
              </Form.Item>
              <span style={{ fontSize: 12, color: '#999999' }}>{_l('开启后按提醒间隔重复发送')}</span>
              <Form.Item name="remindInterval" noStyle>
                <InputNumber min={0} addonAfter="s" style={{ width: 130 }} placeholder="21600" />
              </Form.Item>
            </Space>
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

      {/* Instances silence modal */}
      <InstancesSilenceModal
        open={instancesOpen}
        onClose={() => setInstancesOpen(false)}
        title={instancesRecord?.name}
        objectLabel={_l('节点')}
        instances={instancesList}
        loading={instancesLoading}
        refreshing={instancesRefreshing}
        onRefresh={refreshInstancesList}
        onCreateSilence={handleCreateSilence}
        onCancelSilence={handleCancelSilence}
        formatValue={(inst) => formatInstanceValue(instancesRecord?.monitorType, inst)}
        emptyText={_l('暂无节点数据')}
      />
    </div>
  );
};

export default MongoMonitor;
