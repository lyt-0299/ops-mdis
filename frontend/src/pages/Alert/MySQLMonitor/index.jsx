/**
 * MySQLMonitor — MySQL 监控页
 *
 * monitorType 枚举（camelCase 字段名，与 Go model 一致）：
 *   connectivity        连通性检查（所有架构）
 *   replication_lag     主从延迟检查（replication 架构）
 *   replication_status  主从复制状态检查（replication 架构）
 *   mgr_node_status     MGR 节点状态（mgr 架构）
 *   mgr_sync_status     MGR 同步队列（mgr 架构）
 *
 * camelCase 字段：mysqlInstanceId, monitorType, replicationLagThreshold,
 *   mgrQueueThreshold, duration, recoveryDuration, interval, alertNote,
 *   remark, webhookId, alertState, repeatAlert, remindInterval, etc.
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
  cancelMysqlMonitorSilence,
  createMysqlMonitor,
  createMysqlMonitorSilences,
  deleteMysqlMonitor,
  getMysqlMonitorAlertHistory,
  listMysqlInstances,
  listMysqlMonitorInstances,
  listMysqlMonitors,
  listWebhooks,
  pauseMysqlMonitor,
  refreshMysqlMonitor,
  resumeMysqlMonitor,
  updateMysqlMonitor,
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

const ARCH_LABELS = {
  standalone: _l('单机'),
  replication: _l('主从复制'),
  mgr: 'MGR',
};

const ALL_MONITOR_TYPE_LABELS = {
  connectivity: _l('连通性检查'),
  replication_lag: _l('主从延迟检查'),
  replication_status: _l('主从复制状态检查'),
  mgr_node_status: 'MGR ' + _l('节点状态'),
  mgr_sync_status: 'MGR ' + _l('同步队列'),
};

/** Get available monitor types based on instance architecture. */
function getMonitorTypeOptions(architecture) {
  const base = [{ value: 'connectivity', label: _l('连通性检查') }];
  if (architecture === 'replication') {
    return [
      ...base,
      { value: 'replication_lag', label: _l('主从延迟检查') },
      { value: 'replication_status', label: _l('主从复制状态检查') },
    ];
  }
  if (architecture === 'mgr') {
    return [
      ...base,
      { value: 'mgr_node_status', label: 'MGR ' + _l('节点状态') },
      { value: 'mgr_sync_status', label: 'MGR ' + _l('同步队列') },
    ];
  }
  return base;
}

function defaultMonitorType(architecture) {
  if (architecture === 'replication') return 'replication_lag';
  if (architecture === 'mgr') return 'mgr_node_status';
  return 'connectivity';
}

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

function formatTime(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  } catch { return iso; }
}

/**
 * Render lastDetail cell for MySQL monitors.
 * The lastDetail JSON structure varies by monitorType.
 */
function renderLastDetail(rule) {
  const isPaused = rule.status === 'paused';
  const dimColor = isPaused ? '#bbbbbb' : '#555';

  if (!rule.lastDetail) {
    if (rule.lastValue != null) {
      return (
        <span style={{ fontFamily: 'monospace', fontSize: 12, color: dimColor }}>
          {Number(rule.lastValue).toFixed(2)}
        </span>
      );
    }
    return <span style={{ color: '#bbbbbb' }}>-</span>;
  }

  try {
    const detail = JSON.parse(rule.lastDetail);

    // connectivity / replication_status / mgr_node_status: array of node check results
    if (Array.isArray(detail)) {
      if (detail.length === 0) return <span style={{ color: '#bbbbbb', fontSize: 12 }}>-</span>;
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {detail.slice(0, 5).map((item, idx) => {
            const nodeKey = item.node || item.host || item.member_name || `node-${idx}`;
            const ok = item.ok ?? item.reachable ?? item.healthy ?? true;
            const valueStr = item.value != null
              ? `${item.value}`
              : item.state || item.status || '';
            return (
              <div
                key={idx}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '3px 8px',
                  borderRadius: 6,
                  border: '1px solid #f0f0f0',
                  background: isPaused ? '#fafafa' : (ok ? '#f6ffed' : '#fff1f0'),
                  fontSize: 12,
                  minWidth: 180,
                }}
              >
                <span
                  style={{ fontFamily: 'monospace', color: dimColor, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', marginRight: 8 }}
                  title={nodeKey}
                >
                  {nodeKey}
                </span>
                {valueStr ? (
                  <span style={{ fontFamily: 'monospace', fontWeight: 600, color: isPaused ? '#bbbbbb' : (ok ? '#059669' : '#ff4d4f') }}>
                    {valueStr}
                  </span>
                ) : (
                  <Tag color={isPaused ? 'default' : (ok ? 'success' : 'error')} style={{ fontSize: 11, margin: 0 }}>
                    {ok ? 'OK' : _l('异常')}
                  </Tag>
                )}
              </div>
            );
          })}
          {detail.length > 5 && (
            <span style={{ fontSize: 11, color: '#bbbbbb' }}>…{_l('共')} {detail.length} {_l('个节点')}</span>
          )}
        </div>
      );
    }

    // replication_lag: single value (seconds)
    if (typeof detail === 'object' && detail !== null) {
      const lag = detail.lag_seconds ?? detail.lag ?? null;
      if (lag != null) {
        return (
          <span style={{ fontFamily: 'monospace', fontWeight: 600, fontSize: 12, color: isPaused ? '#bbbbbb' : '#d97706' }}>
            {Number(lag).toFixed(1)}s
          </span>
        );
      }
    }
  } catch { /* fall through */ }

  // Numeric fallback
  if (rule.lastValue != null) {
    return (
      <span style={{ fontFamily: 'monospace', fontSize: 12, color: dimColor }}>
        {Number(rule.lastValue).toFixed(2)}
      </span>
    );
  }
  return <span style={{ color: '#bbbbbb' }}>-</span>;
}

function formatInstanceValue(monitorType, inst) {
  if (monitorType === 'replication_lag') return `${Number(inst.lastValue).toFixed(1)}s`;
  if (monitorType === 'mgr_sync_status') return `${Number(inst.lastValue).toFixed(0)}`;
  return inst.lastValue >= 1 ? 'OK' : _l('异常');
}

const emptyForm = () => ({
  name: '',
  mysqlInstanceId: undefined,
  monitorType: 'connectivity',
  replicationLagThreshold: 300,
  mgrQueueThreshold: 100,
  duration: 120,
  recoveryDuration: 60,
  interval: 60,
  alertNote: '',
  remark: '',
  webhookId: undefined,
  repeatAlert: false,
  remindInterval: 21600,
});

// ── Main component ─────────────────────────────────────────────────────────────

const MySQLMonitor = () => {
  const [loading, setLoading] = useState(false);
  const [rules, setRules] = useState([]);
  const [mysqlInstances, setMysqlInstances] = useState([]);
  const [webhooks, setWebhooks] = useState([]);

  // Filters
  const [filterState, setFilterState] = useState('');
  const [filterSearch, setFilterSearch] = useState('');

  // CRUD modal
  const [modalVisible, setModalVisible] = useState(false);
  const [editingRecord, setEditingRecord] = useState(null);
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();
  const watchedInstanceId = Form.useWatch('mysqlInstanceId', form);
  const watchedMonitorType = Form.useWatch('monitorType', form);

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

  // ── Data fetching ────────────────────────────────────────────────────────────

  const fetchRules = useCallback(() => {
    setLoading(true);
    listMysqlMonitors()
      .then(({ data }) => setRules(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 MySQL 监控列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    setLoading(true);
    Promise.all([listMysqlMonitors(), listMysqlInstances(), listWebhooks()])
      .then(([rulesRes, instRes, whRes]) => {
        setRules(Array.isArray(rulesRes.data) ? rulesRes.data : []);
        setMysqlInstances(Array.isArray(instRes.data) ? instRes.data : []);
        setWebhooks(Array.isArray(whRes.data) ? whRes.data : []);
      })
      .catch((e) => message.error(apiErr(e, _l('数据加载失败'))))
      .finally(() => setLoading(false));
  }, []);

  const getInstanceById = (id) => mysqlInstances.find((i) => i.id === id);
  const getInstanceName = (id) => getInstanceById(id)?.name || '-';
  const getWebhookName = (id) => webhooks.find((w) => w.id === id)?.name || '-';

  // Derive architecture for the currently selected instance in the form
  const selectedArch = useMemo(() => {
    if (!watchedInstanceId) return 'standalone';
    return getInstanceById(watchedInstanceId)?.architecture || 'standalone';
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [watchedInstanceId, mysqlInstances]);

  const monitorTypeOptions = useMemo(
    () => getMonitorTypeOptions(selectedArch),
    [selectedArch],
  );

  // ── Filtered data ─────────────────────────────────────────────────────────────

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
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        const instName = getInstanceName(r.mysqlInstanceId).toLowerCase();
        if (!r.name?.toLowerCase().includes(q) && !instName.includes(q)) return false;
      }
      return true;
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rules, filterState, filterSearch, mysqlInstances]);

  // ── CRUD modal ────────────────────────────────────────────────────────────────

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
      name: record.name,
      mysqlInstanceId: record.mysqlInstanceId,
      monitorType: record.monitorType || 'connectivity',
      replicationLagThreshold: record.replicationLagThreshold ?? 300,
      mgrQueueThreshold: record.mgrQueueThreshold ?? 100,
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

  const handleInstanceChange = (instId) => {
    const arch = getInstanceById(instId)?.architecture || 'standalone';
    const newType = defaultMonitorType(arch);
    form.setFieldsValue({ monitorType: newType });
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);
      const payload = {
        name: values.name,
        mysqlInstanceId: values.mysqlInstanceId,
        monitorType: values.monitorType,
        replicationLagThreshold: values.replicationLagThreshold ?? 300,
        mgrQueueThreshold: values.mgrQueueThreshold ?? 100,
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
        ? updateMysqlMonitor(editingRecord.id, payload)
        : createMysqlMonitor(payload);
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
    deleteMysqlMonitor(record.id)
      .then(() => { message.success(_l('删除成功')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  // ── Refresh ───────────────────────────────────────────────────────────────────

  const handleRefresh = (record) => {
    setRefreshingId(record.id);
    refreshMysqlMonitor(record.id)
      .then(() => { message.success(_l('已触发立即检查')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('立即检查失败'))))
      .finally(() => setRefreshingId(null));
  };

  // ── Pause / Resume ────────────────────────────────────────────────────────────

  const openPause = (record) => { setPauseRecord(record); setPauseOpen(true); };
  const handleResume = (record) => {
    resumeMysqlMonitor(record.id)
      .then(() => { message.success(_l('已启用')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('启用失败，请重试'))));
  };

  const handlePauseConfirm = (payload) => {
    if (!pauseRecord) return;
    setPauseLoading(true);
    pauseMysqlMonitor(pauseRecord.id, payload)
      .then(() => { message.success(_l('已暂停')); setPauseOpen(false); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('暂停失败，请重试'))))
      .finally(() => setPauseLoading(false));
  };

  // ── History ───────────────────────────────────────────────────────────────────

  const openHistory = (record) => { setHistoryRecord(record); setHistoryOpen(true); };
  const fetchHistory = useCallback(
    (page, pageSize) => {
      if (!historyRecord) return Promise.resolve({ data: { items: [], total: 0 } });
      return getMysqlMonitorAlertHistory(historyRecord.id, page, pageSize);
    },
    [historyRecord],
  );

  // ── Instances silence ─────────────────────────────────────────────────────────

  const openInstances = (record) => {
    setInstancesRecord(record);
    setInstancesList([]);
    setInstancesLoading(true);
    setInstancesOpen(true);
    listMysqlMonitorInstances(record.id)
      .then(({ data }) => setInstancesList(Array.isArray(data?.items) ? data.items : []))
      .catch((e) => message.error(apiErr(e, _l('加载实例明细失败'))))
      .finally(() => setInstancesLoading(false));
  };

  const refreshInstancesList = () => {
    if (!instancesRecord || instancesRefreshing) return;
    setInstancesRefreshing(true);
    refreshMysqlMonitor(instancesRecord.id)
      .then(() => listMysqlMonitorInstances(instancesRecord.id))
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
      await createMysqlMonitorSilences(instancesRecord.id, payload);
      message.success(_l('已静默该实例'));
      const { data } = await listMysqlMonitorInstances(instancesRecord.id);
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
      await cancelMysqlMonitorSilence(instancesRecord.id, inst.scopeKey);
      message.success(_l('已取消静默'));
      const { data } = await listMysqlMonitorInstances(instancesRecord.id);
      setInstancesList(Array.isArray(data?.items) ? data.items : []);
      fetchRules();
      return true;
    } catch {
      message.error(_l('取消静默失败'));
      return false;
    }
  };

  // ── Columns ───────────────────────────────────────────────────────────────────

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
        title: 'MySQL ' + _l('实例'),
        ellipsis: true,
        render: (_, r) => {
          const inst = getInstanceById(r.mysqlInstanceId);
          return (
            <span className="sqlInstanceCell">
              <Tag>{inst?.name || '-'}</Tag>
              {inst?.architecture && (
                <span className="sqlInstanceArch">
                  {ARCH_LABELS[inst.architecture] || inst.architecture}
                </span>
              )}
            </span>
          );
        },
      },
      {
        title: _l('监控类型'),
        width: 150,
        render: (_, r) => (
          <Tag>{ALL_MONITOR_TYPE_LABELS[r.monitorType] || r.monitorType}</Tag>
        ),
      },
      {
        title: _l('阈值'),
        width: 110,
        align: 'center',
        render: (_, r) => {
          if (r.monitorType === 'replication_lag') {
            return (
              <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
                &gt; {r.replicationLagThreshold}s
              </span>
            );
          }
          if (r.monitorType === 'mgr_sync_status') {
            return (
              <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
                &gt; {r.mgrQueueThreshold}
              </span>
            );
          }
          return <span style={{ color: '#bbbbbb' }}>-</span>;
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
            {formatTime(r.updatedAt)}
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
    [mysqlInstances, webhooks, refreshingId],
  );

  // ── Render ────────────────────────────────────────────────────────────────────

  const showReplicationThreshold = watchedMonitorType === 'replication_lag';
  const showMgrThreshold = watchedMonitorType === 'mgr_sync_status';

  return (
    <div className="mysqlMonitorContainer">
      {/* Toolbar */}
      <div className="sqlToolbar">
        <div className="sqlFilters">
          <Select
            value={filterState}
            style={{ width: 120 }}
            onChange={setFilterState}
          >
            {STATE_FILTER_OPTIONS.map((o) => (
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
      <div className="sqlTableBox">
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
                description={_l('暂无 MySQL 监控，点击「新建监控」添加')}
              />
            ),
          }}
        />
      </div>

      {/* Create / Edit Modal */}
      <Modal
        title={editingRecord ? _l('编辑 MySQL 监控') : _l('新建 MySQL 监控')}
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
            <Input placeholder={_l('例如：生产环境 MySQL 主从延迟监控')} />
          </Form.Item>

          <Form.Item
            name="mysqlInstanceId"
            label={'MySQL ' + _l('实例')}
            rules={[{ required: true, message: _l('请选择 MySQL 实例') }]}
          >
            <Select
              placeholder={_l('选择 MySQL 实例')}
              notFoundContent={_l('请先在「数据源」页添加对应数据源')}
              onChange={handleInstanceChange}
            >
              {mysqlInstances.map((i) => (
                <Option key={i.id} value={i.id}>
                  {i.name}
                  {i.architecture && (
                    <span style={{ color: '#bbbbbb', fontSize: 11, marginLeft: 6 }}>
                      ({ARCH_LABELS[i.architecture] || i.architecture})
                    </span>
                  )}
                </Option>
              ))}
            </Select>
          </Form.Item>

          <Form.Item
            name="monitorType"
            label={_l('监控类型')}
            rules={[{ required: true, message: _l('请选择监控类型') }]}
          >
            <Select placeholder={_l('选择监控类型')}>
              {monitorTypeOptions.map((o) => (
                <Option key={o.value} value={o.value}>{o.label}</Option>
              ))}
            </Select>
          </Form.Item>

          {showReplicationThreshold && (
            <Form.Item
              name="replicationLagThreshold"
              label={_l('主从延迟阈值（秒）')}
              rules={[{ required: true, message: _l('请输入延迟阈值') }]}
            >
              <InputNumber min={1} addonAfter="s" style={{ width: 200 }} placeholder="300" />
            </Form.Item>
          )}

          {showMgrThreshold && (
            <Form.Item
              name="mgrQueueThreshold"
              label={'MGR ' + _l('同步队列阈值')}
              rules={[{ required: true, message: _l('请输入队列阈值') }]}
            >
              <InputNumber min={1} style={{ width: 200 }} placeholder="100" />
            </Form.Item>
          )}

          <Form.Item label={_l('检查频率 / 告警持续 / 恢复持续（秒）')} style={{ marginBottom: 0 }}>
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

export default MySQLMonitor;
