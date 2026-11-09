import React, { useCallback,useEffect, useMemo, useState } from 'react';
import { PlusOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
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
  createSslMonitor,
  deleteSslMonitor,
  getSslMonitorAlertHistory,
  listSslMonitors,
  listWebhooks,
  pauseSslMonitor,
  refreshSslMonitor,
  resumeSslMonitor,
  updateSslMonitor,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

import AlertHistoryModal from '../components/AlertHistoryModal';
import PauseModal        from '../components/PauseModal';

import './index.less';

const { Option } = Select;
const { TextArea } = Input;

// ── Alert state tags ─────────────────────────────────────────────────────────

const ALERT_STATE_TAG = {
  OK:        { color: 'success',    text: 'OK' },
  Pending:   { color: 'warning',    text: _l('预警') },
  Firing:    { color: 'error',      text: _l('告警') },
  Recovered: { color: 'processing', text: _l('恢复') },
};

const STATE_FILTER_OPTIONS = [
  { value: '',                   label: _l('全部状态') },
  { value: 'active_OK',          label: 'OK' },
  { value: 'active_Pending',     label: _l('预警') },
  { value: 'active_Firing',      label: _l('告警') },
  { value: 'active_Recovered',   label: _l('恢复') },
  { value: 'paused',             label: _l('已暂停') },
];

// ── Helpers ───────────────────────────────────────────────────────────────────

function formatDate(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  } catch { return iso; }
}

function formatTime(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  } catch { return iso; }
}

function formatDuration(secs) {
  if (!secs && secs !== 0) return '-';
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.round(secs / 60)}m`;
  if (secs < 86400) return `${Math.round(secs / 3600)}h`;
  return `${Math.round(secs / 86400)}d`;
}

const emptyForm = () => ({
  name:            '',
  domain:          '',
  port:            443,
  ignoreSslErrors: false,
  timeout:         10,
  warnDays:        14,
  interval:        86400,
  webhookId:       undefined,
  alertNote:       '',
});

// ── Main component ────────────────────────────────────────────────────────────

const SslMonitor = () => {
  const [loading,       setLoading]       = useState(false);
  const [monitors,      setMonitors]      = useState([]);
  const [webhooks,      setWebhooks]      = useState([]);

  // Filters
  const [filterState,   setFilterState]   = useState('');
  const [filterWebhook, setFilterWebhook] = useState('');
  const [filterSearch,  setFilterSearch]  = useState('');

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

  // ── Data fetching ──────────────────────────────────────────────────────────

  const fetchMonitors = useCallback(() => {
    setLoading(true);
    listSslMonitors()
      .then(({ data }) => setMonitors(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 SSL 监控列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    setLoading(true);
    Promise.all([listSslMonitors(), listWebhooks()])
      .then(([monRes, whRes]) => {
        setMonitors(Array.isArray(monRes.data) ? monRes.data : []);
        setWebhooks(Array.isArray(whRes.data) ? whRes.data : []);
      })
      .catch((e) => message.error(apiErr(e, _l('数据加载失败'))))
      .finally(() => setLoading(false));
  }, []);

  const getWebhookName = (id) => webhooks.find((w) => w.id === id)?.name || '-';

  // ── Filtered data ──────────────────────────────────────────────────────────

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
      if (filterWebhook) {
        if (filterWebhook === 'none' && r.webhookId != null)       return false;
        if (filterWebhook !== 'none' && r.webhookId !== filterWebhook) return false;
      }
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        if (!r.name?.toLowerCase().includes(q) && !r.domain?.toLowerCase().includes(q)) return false;
      }
      return true;
    });
  }, [monitors, filterState, filterWebhook, filterSearch]);

  // ── CRUD modal ─────────────────────────────────────────────────────────────

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
      domain:          record.domain,
      port:            record.port ?? 443,
      ignoreSslErrors: record.ignoreSslErrors ?? false,
      timeout:         record.timeout ?? 10,
      warnDays:        record.warnDays ?? 14,
      interval:        record.interval ?? 86400,
      webhookId:       record.webhookId,
      alertNote:       record.alertNote || '',
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
      const req = editingRecord
        ? updateSslMonitor(editingRecord.id, values)
        : createSslMonitor(values);
      req
        .then(() => { message.success(editingRecord ? _l('更新成功') : _l('创建成功')); closeModal(); fetchMonitors(); })
        .catch((e) => message.error(apiErr(e, editingRecord ? '更新失败，请重试' : '创建失败，请重试')))
        .finally(() => setConfirmLoading(false));
    }).catch(() => setConfirmLoading(false));
  };

  const handleDelete = (record) => {
    deleteSslMonitor(record.id)
      .then(() => { message.success(_l('删除成功')); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  const handleRefresh = (record) => {
    setRefreshingId(record.id);
    refreshSslMonitor(record.id)
      .then(() => { message.success(_l('已触发立即检查')); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('立即检查失败'))))
      .finally(() => setRefreshingId(null));
  };

  const openPause    = (record) => { setPauseRecord(record); setPauseOpen(true); };
  const handleResume = (record) => {
    resumeSslMonitor(record.id)
      .then(() => { message.success(_l('已启用')); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('启用失败，请重试'))));
  };

  const handlePauseConfirm = (payload) => {
    if (!pauseRecord) return;
    setPauseLoading(true);
    pauseSslMonitor(pauseRecord.id, payload)
      .then(() => { message.success(_l('已暂停')); setPauseOpen(false); fetchMonitors(); })
      .catch((e) => message.error(apiErr(e, _l('暂停失败，请重试'))))
      .finally(() => setPauseLoading(false));
  };

  const openHistory  = (record) => { setHistoryRecord(record); setHistoryOpen(true); };
  const fetchHistory = useCallback(
    (page, pageSize) => {
      if (!historyRecord) return Promise.resolve({ data: { items: [], total: 0 } });
      return getSslMonitorAlertHistory(historyRecord.id, page, pageSize);
    },
    [historyRecord],
  );

  // ── Columns ────────────────────────────────────────────────────────────────

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
      { title: _l('任务名'),    dataIndex: 'name',   ellipsis: true },
      {
        title: _l('域名'),
        dataIndex: 'domain',
        ellipsis: true,
        render: (v) => (
          <span>
            <SafetyCertificateOutlined style={{ color: '#64748b', marginRight: 4 }} />
            <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</span>
          </span>
        ),
      },
      { title: _l('端口'), dataIndex: 'port', width: 80, render: (v) => <Tag>{v}</Tag> },
      {
        title: _l('剩余天数'),
        width: 100,
        align: 'center',
        render: (_, r) => {
          if (r.daysLeft == null) return <span style={{ color: '#bbbbbb' }}>-</span>;
          const color = r.daysLeft <= 7 ? '#f5222d' : r.daysLeft <= 30 ? '#fa8c16' : '#52c41a';
          return <span style={{ color, fontWeight: 600, fontFamily: 'monospace' }}>{r.daysLeft}d</span>;
        },
      },
      {
        title: _l('到期时间'),
        width: 120,
        render: (_, r) => <span style={{ fontFamily: 'monospace', fontSize: 11 }}>{formatDate(r.certExpiry)}</span>,
      },
      {
        title: _l('提前告警天数'),
        dataIndex: 'warnDays',
        width: 110,
        align: 'center',
        render: (v) => `${v} ${_l('天')}`,
      },
      { title: _l('检查频率'), width: 100, align: 'center', render: (_, r) => formatDuration(r.interval) },
      {
        title: _l('最近检查'),
        width: 145,
        render: (_, r) => <span style={{ fontFamily: 'monospace', fontSize: 11 }}>{formatTime(r.lastCheckAt)}</span>,
      },
      {
        title: _l('通知渠道'),
        width: 130,
        ellipsis: true,
        render: (_, r) => r.webhookId
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
    [webhooks, refreshingId],
  );

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="sslMonitorContainer">
      {/* ── Toolbar ──────────────────────────────────────────────────── */}
      <div className="smToolbar">
        <div className="smFilters">
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
            placeholder={_l('搜索名称或域名')}
            value={filterSearch}
            onChange={(e) => setFilterSearch(e.target.value)}
            onSearch={(v) => setFilterSearch(v)}
            style={{ width: 200 }}
            allowClear
          />

          <Button
            onClick={() => { setFilterState(''); setFilterWebhook(''); setFilterSearch(''); }}
          >
            {_l('重置')}
          </Button>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          {_l('新建监控')}
        </Button>
      </div>

      {/* ── Table ────────────────────────────────────────────────────── */}
      <div className="smTableBox">
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
                description={_l('暂无 SSL 证书监控，点击「新建监控」添加')}
              />
            ),
          }}
        />
      </div>

      {/* ── Create / Edit Modal ─────────────────────────────────────── */}
      <Modal
        title={editingRecord ? _l('编辑 SSL 监控') : _l('新建 SSL 监控')}
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
            <Input placeholder={_l('例如：官网 HTTPS 证书')} />
          </Form.Item>

          <Form.Item
            name="domain"
            label={_l('域名')}
            rules={[{ required: true, message: _l('请输入域名') }]}
          >
            <Input
              placeholder="example.com"
              style={{ fontFamily: 'monospace' }}
              addonBefore="https://"
            />
          </Form.Item>

          <Form.Item label={_l('端口 / 超时（秒）')} style={{ marginBottom: 0 }}>
            <Space>
              <Form.Item name="port" noStyle>
                <InputNumber min={1} max={65535} addonBefore="Port" style={{ width: 140 }} placeholder="443" />
              </Form.Item>
              <Form.Item name="timeout" noStyle>
                <InputNumber min={1} max={60} addonAfter="s" style={{ width: 130 }} placeholder="10" />
              </Form.Item>
            </Space>
          </Form.Item>

          <Form.Item
            name="warnDays"
            label={_l('提前告警天数')}
            extra={_l('证书到期前多少天开始告警')}
            style={{ marginTop: 16 }}
          >
            <InputNumber min={1} max={90} addonAfter={_l('天')} style={{ width: 160 }} placeholder="14" />
          </Form.Item>

          <Form.Item
            name="interval"
            label={_l('检查频率（秒）')}
            extra={_l('默认 86400 s（每天一次）')}
          >
            <InputNumber min={3600} addonAfter="s" style={{ width: 160 }} placeholder="86400" />
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

      {/* ── Alert history modal ─────────────────────────────────────── */}
      <AlertHistoryModal
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        title={historyRecord?.name}
        fetchHistory={fetchHistory}
      />

      {/* ── Pause modal ─────────────────────────────────────────────── */}
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

export default SslMonitor;
