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
  cancelAlertRuleSilence,
  createAlertRule,
  createAlertRuleSilences,
  deleteAlertRule,
  getAlertRuleAlertHistory,
  getMetricTemplates,
  listAlertRuleInstances,
  listAlertRules,
  listDataSources,
  listWebhooks,
  pauseAlertRule,
  previewPromQL,
  refreshAlertRule,
  resumeAlertRule,
  updateAlertRule,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { getSourceDisplayName } from '@/utils/sourceDisplay';
import { _l } from '@/utils/utils';

import AlertHistoryModal    from '../components/AlertHistoryModal';
import InstancesSilenceModal from '../components/InstancesSilenceModal';
import PauseModal           from '../components/PauseModal';

import './index.less';

const { Option } = Select;
const { TextArea } = Input;

// ── Alert state display ──────────────────────────────────────────────────────

const ALERT_STATE_TAG = {
  OK:        { color: 'success',    text: 'OK' },
  Pending:   { color: 'warning',    text: _l('预警') },
  Firing:    { color: 'error',      text: _l('告警') },
  Recovered: { color: 'processing', text: _l('恢复') },
};

// ── Operators ────────────────────────────────────────────────────────────────

const OPERATORS = [
  { value: '>',  label: `> ${_l('大于')}` },
  { value: '>=', label: `≥ ${_l('大于等于')}` },
  { value: '<',  label: `< ${_l('小于')}` },
  { value: '<=', label: `≤ ${_l('小于等于')}` },
  { value: '==', label: `= ${_l('等于')}` },
  { value: '!=', label: `≠ ${_l('不等于')}` },
];

// ── Filter state options ──────────────────────────────────────────────────────

const STATE_FILTER_OPTIONS = [
  { value: '',            label: _l('全部状态') },
  { value: 'active_OK',   label: 'OK' },
  { value: 'active_Pending',  label: _l('预警') },
  { value: 'active_Firing',   label: _l('告警') },
  { value: 'active_Recovered',label: _l('恢复') },
  { value: 'paused',      label: _l('已暂停') },
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

function getMetricCategoryLabel(category) {
  switch (category) {
    case 'server': return _l('服务器监控');
    case 'kubernetes': return _l('Kubernetes 集群');
    case 'custom': return _l('自定义 PromQL');
    default: return category || '-';
  }
}

function getMetricCategoryDescription(category) {
  switch (category) {
    case 'server': return _l('基于 node_exporter 的服务器基础资源监控');
    case 'kubernetes': return _l('集群上报状态与节点健康，多集群按 origin_prometheus 区分');
    case 'custom': return _l('直接编写自定义 PromQL 表达式进行高级监控');
    default: return '';
  }
}

function getMetricTemplateLabel(type) {
  switch (type) {
    case 'cpu': return _l('CPU 使用率');
    case 'memory': return _l('内存使用率');
    case 'disk': return _l('磁盘使用率');
    case 'disk_io': return _l('磁盘 IO 使用率');
    case 'online': return _l('在线状态');
    case 'network_in': return _l('网络入流量');
    case 'network_out': return _l('网络出流量');
    case 'custom_query': return _l('自定义查询');
    default: return type || '-';
  }
}

function getMetricTemplateDescription(type) {
  switch (type) {
    case 'cpu': return _l('服务器 CPU 使用率（所有核心平均值）');
    case 'memory': return _l('服务器内存使用率');
    case 'disk': return _l('服务器根分区磁盘使用率');
    case 'disk_io': return _l('服务器磁盘 IO 繁忙程度');
    case 'online': return _l('基于 Prometheus up 指标判断目标在线状态（1=在线，0=离线），默认当 up=0（离线）时告警');
    case 'network_in': return _l('服务器网络接收速率');
    case 'network_out': return _l('服务器网络发送速率');
    case 'custom_query': return _l('手动编写 PromQL 表达式，支持任意复杂的监控场景');
    default: return '';
  }
}

function localizeMetricCategories(categories) {
  if (!Array.isArray(categories)) return [];
  return categories.map((cat) => ({
    ...cat,
    name: getMetricCategoryLabel(cat.category),
    description: getMetricCategoryDescription(cat.category),
    templates: (Array.isArray(cat.templates) ? cat.templates : []).map((tpl) => ({
      ...tpl,
      name: getMetricTemplateLabel(tpl.type),
      description: getMetricTemplateDescription(tpl.type),
    })),
  }));
}

// ── Default form values ───────────────────────────────────────────────────────

const emptyForm = () => ({
  name: '',
  dataSourceId: undefined,
  category: 'custom',
  metricType: '',
  promql: '',
  operator: '>',
  threshold: 80,
  unit: '',
  interval: 60,
  duration: 90,
  recoveryDuration: 60,
  webhookId: undefined,
  alertNote: '',
});

// ── Main component ────────────────────────────────────────────────────────────

const AlertRules = () => {
  const [loading,       setLoading]       = useState(false);
  const [rules,         setRules]         = useState([]);
  const [dataSources,   setDataSources]   = useState([]);
  const [webhooks,      setWebhooks]      = useState([]);
  const [metricCats,    setMetricCats]    = useState([]); // [{category, name, templates:[{type,name,unit}]}]

  // Filters
  const [filterState,     setFilterState]     = useState('');
  const [filterDatasource,setFilterDatasource] = useState('');
  const [filterCategory,  setFilterCategory]  = useState('');
  const [filterWebhook,   setFilterWebhook]   = useState('');
  const [filterSearch,    setFilterSearch]    = useState('');

  // CRUD modal
  const [modalVisible, setModalVisible] = useState(false);
  const [editingRecord, setEditingRecord] = useState(null);
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();
  const selectedCategory = Form.useWatch('category', form);

  // PromQL preview
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewOpen,    setPreviewOpen]    = useState(false);
  const [previewResult,  setPreviewResult]  = useState(null); // { resultType, count, series }

  // Refreshing row state
  const [refreshingId, setRefreshingId] = useState(null);

  // History modal
  const [historyOpen,   setHistoryOpen]   = useState(false);
  const [historyRecord, setHistoryRecord] = useState(null);

  // Instances modal
  const [instancesOpen,       setInstancesOpen]       = useState(false);
  const [instancesRecord,     setInstancesRecord]     = useState(null);
  const [instances,           setInstances]           = useState([]);
  const [instancesLoading,    setInstancesLoading]    = useState(false);
  const [instancesRefreshing, setInstancesRefreshing] = useState(false);

  // Pause modal
  const [pauseOpen,    setPauseOpen]    = useState(false);
  const [pauseRecord,  setPauseRecord]  = useState(null);
  const [pauseLoading, setPauseLoading] = useState(false);

  // ── Data fetching ──────────────────────────────────────────────────────────

  const fetchRules = useCallback(() => {
    setLoading(true);
    listAlertRules()
      .then(({ data }) => setRules(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取监控列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    // Parallel: load rules + supporting lists
    setLoading(true);
    Promise.all([
      listAlertRules(),
      listDataSources(),
      listWebhooks(),
      getMetricTemplates(),
    ])
      .then(([rulesRes, dsRes, whRes, tplRes]) => {
        setRules(Array.isArray(rulesRes.data) ? rulesRes.data : []);
        setDataSources(Array.isArray(dsRes.data) ? dsRes.data : []);
        setWebhooks(Array.isArray(whRes.data) ? whRes.data : []);
        setMetricCats(localizeMetricCategories(tplRes.data));
      })
      .catch((e) => message.error(apiErr(e, _l('数据加载失败'))))
      .finally(() => setLoading(false));
  }, []);

  // ── Lookup helpers ─────────────────────────────────────────────────────────

  const getDsName = (id) => {
    const source = dataSources.find((d) => d.id === id);
    return source ? getSourceDisplayName(source) : id || '-';
  };
  const getWebhookName = (id) => webhooks.find((w) => w.id === id)?.name || '-';

  const getMetricName = (category, metricType) => {
    if (category === 'custom') return _l('自定义 PromQL');
    const cat = metricCats.find((c) => c.category === category);
    if (cat) {
      const tpl = cat.templates?.find((t) => t.type === metricType);
      if (tpl) return tpl.name;
    }
    return metricType || '-';
  };

  const getMetricUnit = (category, metricType) => {
    if (category === 'custom') return '';
    const cat = metricCats.find((c) => c.category === category);
    if (cat) {
      const tpl = cat.templates?.find((t) => t.type === metricType);
      if (tpl) return tpl.unit || '';
    }
    return '';
  };

  // ── Filtered + sorted data ─────────────────────────────────────────────────

  const filteredRules = useMemo(() => {
    return rules.filter((rule) => {
      if (filterState) {
        if (filterState === 'paused' && rule.status !== 'paused') return false;
        if (filterState !== 'paused') {
          if (rule.status === 'paused') return false;
          const stateType = filterState.replace('active_', '');
          if (rule.alertState !== stateType) return false;
        }
      }
      if (filterDatasource && rule.dataSourceId !== filterDatasource) return false;
      if (filterCategory   && rule.category       !== filterCategory)   return false;
      if (filterWebhook) {
        if (filterWebhook === 'none' && rule.webhookId != null)       return false;
        if (filterWebhook !== 'none' && rule.webhookId !== filterWebhook) return false;
      }
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        if (!rule.name?.toLowerCase().includes(q)) return false;
      }
      return true;
    });
  }, [rules, filterState, filterDatasource, filterCategory, filterWebhook, filterSearch]);

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
      name:             record.name,
      dataSourceId:     record.dataSourceId,
      category:         record.category || 'custom',
      metricType:       record.metricType || '',
      promql:           record.promql || '',
      operator:         record.operator || '>',
      threshold:        record.threshold ?? 80,
      unit:             record.unit || '',
      interval:         record.interval || 60,
      duration:         record.duration || 90,
      recoveryDuration: record.recoveryDuration || 60,
      webhookId:        record.webhookId,
      alertNote:        record.alertNote || '',
    });
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setEditingRecord(null);
    form.resetFields();
  };

  // ── PromQL preview ───────────────────────────────────────────────────────

  const handlePreviewPromQL = () => {
    const promql = (form.getFieldValue('promql') || '').trim();
    const dataSourceId = form.getFieldValue('dataSourceId');
    if (!promql) {
      message.warning(_l('请先输入 PromQL 表达式'));
      return;
    }
    const payload = { promql };
    if (dataSourceId) payload.dataSourceId = dataSourceId;

    setPreviewLoading(true);
    previewPromQL(payload)
      .then((res) => {
        const body = res.data || {};
        if (body.ok === true) {
          setPreviewResult({
            resultType: body.resultType,
            count: body.count,
            series: Array.isArray(body.series) ? body.series : [],
          });
          setPreviewOpen(true);
        } else {
          message.error(body.message || _l('查询失败'));
        }
      })
      .catch((e) => message.error(apiErr(e, _l('预览请求失败，请检查网络或数据源'))))
      .finally(() => setPreviewLoading(false));
  };

  // metric labels object → `{k="v", ...}` string
  const formatMetricLabels = (metric) => {
    if (!metric || typeof metric !== 'object') return '{}';
    const parts = Object.keys(metric).map((k) => `${k}="${metric[k]}"`);
    return `{${parts.join(', ')}}`;
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);

      const payload = {
        name:             values.name,
        dataSourceId:     values.dataSourceId,
        category:         values.category || 'custom',
        metricType:       values.category === 'custom' ? '' : (values.metricType || ''),
        promql:           values.category === 'custom' ? values.promql : '',
        operator:         values.operator,
        threshold:        values.threshold,
        unit:             values.unit || '',
        interval:         values.interval,
        duration:         values.duration,
        recoveryDuration: values.recoveryDuration,
        webhookId:        values.webhookId || null,
        alertNote:        values.alertNote || '',
      };

      const req = editingRecord
        ? updateAlertRule(editingRecord.id, payload)
        : createAlertRule(payload);

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

  // ── Delete ─────────────────────────────────────────────────────────────────

  const handleDelete = (record) => {
    deleteAlertRule(record.id)
      .then(() => { message.success(_l('删除成功')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  // ── Refresh ────────────────────────────────────────────────────────────────

  const handleRefresh = (record) => {
    setRefreshingId(record.id);
    refreshAlertRule(record.id)
      .then(() => { message.success(_l('已触发立即检查')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('立即检查失败'))))
      .finally(() => setRefreshingId(null));
  };

  // ── Pause / Resume ─────────────────────────────────────────────────────────

  const openPause = (record) => {
    setPauseRecord(record);
    setPauseOpen(true);
  };

  const handlePauseConfirm = (payload) => {
    if (!pauseRecord) return;
    setPauseLoading(true);
    pauseAlertRule(pauseRecord.id, payload)
      .then(() => { message.success(_l('已暂停')); setPauseOpen(false); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('暂停失败，请重试'))))
      .finally(() => setPauseLoading(false));
  };

  const handleResume = (record) => {
    resumeAlertRule(record.id)
      .then(() => { message.success(_l('已启用')); fetchRules(); })
      .catch((e) => message.error(apiErr(e, _l('启用失败，请重试'))));
  };

  // ── Alert history modal ────────────────────────────────────────────────────

  const openHistory = (record) => {
    setHistoryRecord(record);
    setHistoryOpen(true);
  };

  const fetchHistory = useCallback(
    (page, pageSize) => {
      if (!historyRecord) return Promise.resolve({ data: { items: [], total: 0 } });
      return getAlertRuleAlertHistory(historyRecord.id, page, pageSize);
    },
    [historyRecord],
  );

  // ── Instances modal ────────────────────────────────────────────────────────

  const openInstances = (record) => {
    setInstancesRecord(record);
    setInstances([]);
    setInstancesOpen(true);
    setInstancesLoading(true);
    listAlertRuleInstances(record.id)
      .then(({ data }) => setInstances(Array.isArray(data?.items) ? data.items : []))
      .catch((e) => message.error(apiErr(e, _l('获取实例列表失败'))))
      .finally(() => setInstancesLoading(false));
  };

  const handleInstancesRefresh = () => {
    if (!instancesRecord || instancesRefreshing) return;
    setInstancesRefreshing(true);
    refreshAlertRule(instancesRecord.id)
      .then(() => listAlertRuleInstances(instancesRecord.id))
      .then(({ data }) => {
        setInstances(Array.isArray(data?.items) ? data.items : []);
        message.success(_l('已刷新'));
        fetchRules();
      })
      .catch((e) => message.error(apiErr(e, _l('刷新失败'))))
      .finally(() => setInstancesRefreshing(false));
  };

  const handleCreateSilence = async (payload) => {
    if (!instancesRecord) return false;
    try {
      await createAlertRuleSilences(instancesRecord.id, payload);
      message.success(_l('已静默该实例'));
      // Reload instances
      const { data } = await listAlertRuleInstances(instancesRecord.id);
      setInstances(Array.isArray(data?.items) ? data.items : []);
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
      await cancelAlertRuleSilence(instancesRecord.id, inst.scopeKey);
      message.success(_l('已取消静默'));
      const { data } = await listAlertRuleInstances(instancesRecord.id);
      setInstances(Array.isArray(data?.items) ? data.items : []);
      fetchRules();
      return true;
    } catch {
      message.error(_l('取消静默失败'));
      return false;
    }
  };

  const formatInstanceValue = (inst) => {
    if (!instancesRecord) return '-';
    const unit = getMetricUnit(instancesRecord.category, instancesRecord.metricType);
    if (inst.lastValue == null) return '-';
    return `${Number(inst.lastValue).toFixed(2)}${unit}`;
  };

  // ── Columns ────────────────────────────────────────────────────────────────

  const columns = useMemo(
    () => [
      {
        title: _l('状态'),
        width: 110,
        render: (_, record) => {
          if (record.status === 'paused') return <Tag color="default">{_l('已暂停')}</Tag>;
          const state = record.alertState;
          const cfg = ALERT_STATE_TAG[state] || { color: 'default', text: state };
          return <Tag color={cfg.color}>{cfg.text}</Tag>;
        },
      },
      {
        title: _l('任务名'),
        dataIndex: 'name',
        ellipsis: true,
      },
      {
        title: _l('数据源'),
        dataIndex: 'dataSourceId',
        ellipsis: true,
        render: (id) => <Tag>{getDsName(id)}</Tag>,
      },
      {
        title: _l('指标'),
        ellipsis: true,
        render: (_, record) => (
          <Tooltip title={record.category === 'custom' ? record.promql : undefined}>
            <span>{getMetricName(record.category, record.metricType)}</span>
          </Tooltip>
        ),
      },
      {
        title: _l('当前值'),
        width: 110,
        align: 'right',
        render: (_, record) => {
          const unit = getMetricUnit(record.category, record.metricType);
          if (record.lastValue == null) return <span style={{ color: '#bbbbbb' }}>-</span>;
          return (
            <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
              {Number(record.lastValue).toFixed(2)}{unit}
            </span>
          );
        },
      },
      {
        title: _l('告警阈值'),
        width: 140,
        render: (_, record) => {
          const unit = getMetricUnit(record.category, record.metricType) || record.unit || '';
          return (
            <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
              {record.operator} {record.threshold}{unit ? ` (${unit})` : ''}
            </span>
          );
        },
      },
      {
        title: _l('检查频率'),
        width: 100,
        align: 'center',
        render: (_, record) => formatDuration(record.interval),
      },
      {
        title: _l('持续时间'),
        width: 130,
        align: 'center',
        render: (_, record) => (
          <span>
            {formatDuration(record.duration)} / {formatDuration(record.recoveryDuration)}
          </span>
        ),
      },
      {
        title: _l('最近检查'),
        width: 145,
        render: (_, record) => (
          <span style={{ fontFamily: 'monospace', fontSize: 11 }}>
            {formatTime(record.updatedAt)}
          </span>
        ),
      },
      {
        title: _l('通知渠道'),
        width: 130,
        ellipsis: true,
        render: (_, record) =>
          record.webhookId ? (
            <Tag>{getWebhookName(record.webhookId)}</Tag>
          ) : (
            // 没配渠道时后端只会写告警历史、不通知任何人（日志里那句「跳过发送」）。
            // 原来这里只显示一个灰色 "-"，用户完全看不出规则其实是「哑」的。
            <Tooltip title={_l('未配置通知渠道：规则触发后只写入告警历史，不会通知任何人')}>
              <Tag color="warning">{_l('不通知')}</Tag>
            </Tooltip>
          ),
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
            <ActionIcon type="detail" title={_l('实例明细')} onClick={() => openInstances(record)} />
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
    [dataSources, webhooks, metricCats, refreshingId],
  );

  // ── Metric template options ────────────────────────────────────────────────

  const currentCat = metricCats.find((c) => c.category === selectedCategory);
  const templateOptions = currentCat?.templates || [];

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="alertRulesContainer">
      {/* ── Toolbar ──────────────────────────────────────────────────── */}
      <div className="arToolbar">
        <div className="arFilters">
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
            placeholder={_l('数据源')}
            style={{ width: 160 }}
            value={filterDatasource || undefined}
            onChange={(v) => setFilterDatasource(v || '')}
          >
            {dataSources.map((ds) => (
              <Option key={ds.id} value={ds.id}>{getSourceDisplayName(ds)}</Option>
            ))}
          </Select>

          <Select
            allowClear
            placeholder={_l('指标分类')}
            style={{ width: 180 }}
            value={filterCategory || undefined}
            onChange={(v) => setFilterCategory(v || '')}
          >
            {metricCats.map((c) => (
              <Option key={c.category} value={c.category}>{c.name}</Option>
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
            placeholder={_l('搜索任务名称')}
            value={filterSearch}
            onChange={(e) => setFilterSearch(e.target.value)}
            onSearch={(v) => setFilterSearch(v)}
            style={{ width: 200 }}
            allowClear
          />

          <Button
            onClick={() => {
              setFilterState('');
              setFilterDatasource('');
              setFilterCategory('');
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

      {/* ── Table ────────────────────────────────────────────────────── */}
      <div className="arTableBox">
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
                description={_l('暂无监控任务，点击「新建任务」添加')}
              />
            ),
          }}
        />
      </div>

      {/* ── Create / Edit Modal ────────────────────────────────────── */}
      <Modal
        title={editingRecord ? _l('编辑监控任务') : _l('新建监控任务')}
        open={modalVisible}
        width={640}
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
            <Input placeholder={_l('例如：主机 CPU 高告警')} />
          </Form.Item>

          <Form.Item
            name="dataSourceId"
            label={_l('数据源')}
            rules={[{ required: true, message: _l('请选择数据源') }]}
          >
            <Select placeholder={_l('选择 Prometheus 数据源')}>
              {dataSources.map((ds) => (
                <Option key={ds.id} value={ds.id}>{getSourceDisplayName(ds)}</Option>
              ))}
            </Select>
          </Form.Item>

          {/* Metric category */}
          <Form.Item name="category" label={_l('指标分类')}>
            <Select
              onChange={() => {
                form.setFieldValue('metricType', '');
                form.setFieldValue('promql', '');
              }}
            >
              <Option value="custom">{_l('自定义 PromQL')}</Option>
              {metricCats.map((c) => (
                <Option key={c.category} value={c.category}>{c.name}</Option>
              ))}
            </Select>
          </Form.Item>

          {/* Metric type (preset) */}
          {selectedCategory && selectedCategory !== 'custom' && (
            <Form.Item
              name="metricType"
              label={_l('指标')}
              rules={[{ required: true, message: _l('请选择指标') }]}
            >
              <Select placeholder={_l('选择指标')}>
                {templateOptions.map((t) => (
                  <Option key={t.type} value={t.type}>
                    {t.name}{t.unit ? ` (${t.unit})` : ''}
                  </Option>
                ))}
              </Select>
            </Form.Item>
          )}

          {/* Custom PromQL */}
          {selectedCategory === 'custom' && (
            <Form.Item
              label={
                <Space>
                  <span>PromQL</span>
                  <Button
                    type="link"
                    size="small"
                    style={{ padding: 0, height: 'auto' }}
                    loading={previewLoading}
                    onClick={handlePreviewPromQL}
                  >
                    {_l('预览')}
                  </Button>
                </Space>
              }
              required
            >
              <Form.Item
                name="promql"
                noStyle
                rules={[{ required: true, message: _l('请输入 PromQL 表达式') }]}
              >
                <TextArea
                  rows={3}
                  placeholder='e.g. avg(rate(http_requests_total[5m]))'
                  style={{ fontFamily: 'monospace', fontSize: 12 }}
                />
              </Form.Item>
            </Form.Item>
          )}

          {/* Operator + Threshold + Unit */}
          <Form.Item label={_l('告警阈值')} style={{ marginBottom: 0 }}>
            <Space>
              <Form.Item name="operator" noStyle>
                <Select style={{ width: 140 }}>
                  {OPERATORS.map((o) => (
                    <Option key={o.value} value={o.value}>{o.label}</Option>
                  ))}
                </Select>
              </Form.Item>
              <Form.Item
                name="threshold"
                noStyle
                rules={[{ required: true, message: _l('请输入阈值') }]}
              >
                <InputNumber style={{ width: 130 }} />
              </Form.Item>
              <Form.Item name="unit" noStyle>
                <Input style={{ width: 90 }} placeholder={_l('单位（可选）')} />
              </Form.Item>
            </Space>
          </Form.Item>

          {/* Interval, Duration, Recovery duration */}
          <Form.Item label={_l('检查频率 / 持续时间 / 恢复持续')} style={{ marginBottom: 0, marginTop: 16 }}>
            <Space>
              <Form.Item name="interval" noStyle>
                <InputNumber min={10} addonAfter="s" style={{ width: 120 }} placeholder="60" />
              </Form.Item>
              <Form.Item name="duration" noStyle>
                <InputNumber min={0} addonAfter="s" style={{ width: 120 }} placeholder="90" />
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
            <TextArea
              rows={2}
              placeholder={_l('告警通知附带的说明文字（可选）')}
            />
          </Form.Item>
        </Form>
      </Modal>

      {/* ── PromQL preview modal ────────────────────────────────────── */}
      <Modal
        title={_l('PromQL 预览结果')}
        open={previewOpen}
        width={680}
        footer={null}
        onCancel={() => setPreviewOpen(false)}
        destroyOnClose
      >
        <div style={{ marginBottom: 12 }}>
          <Space size="middle">
            <Tag>{_l('共 %0 条序列', previewResult?.count ?? 0)}</Tag>
            {previewResult?.resultType ? (
              <span style={{ color: '#999999', fontSize: 12 }}>
                {_l('类型')}: {previewResult.resultType}
              </span>
            ) : null}
          </Space>
        </div>
        <Table
          size="small"
          rowKey={(_, idx) => idx}
          dataSource={(previewResult?.series || []).slice(0, 50)}
          pagination={false}
          scroll={{ y: 360 }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('查询无结果')}
              />
            ),
          }}
          columns={[
            {
              title: _l('指标'),
              dataIndex: 'metric',
              render: (metric) => (
                <span style={{ fontFamily: 'monospace', fontSize: 12, wordBreak: 'break-all' }}>
                  {formatMetricLabels(metric)}
                </span>
              ),
            },
            {
              title: _l('值'),
              width: 160,
              align: 'right',
              render: (_, row) => (
                <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
                  {row.valueStr != null ? row.valueStr : row.value}
                </span>
              ),
            },
          ]}
        />
      </Modal>

      {/* ── Alert history modal ─────────────────────────────────────── */}
      <AlertHistoryModal
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        title={historyRecord?.name}
        fetchHistory={fetchHistory}
      />

      {/* ── Instances & silence modal ───────────────────────────────── */}
      <InstancesSilenceModal
        open={instancesOpen}
        onClose={() => setInstancesOpen(false)}
        title={instancesRecord?.name}
        instances={instances}
        loading={instancesLoading}
        refreshing={instancesRefreshing}
        onRefresh={handleInstancesRefresh}
        onCreateSilence={handleCreateSilence}
        onCancelSilence={handleCancelSilence}
        formatValue={formatInstanceValue}
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

export default AlertRules;
