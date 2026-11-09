/**
 * DataSources — 顶级「数据源」聚合管理页
 *
 * 跨类型聚合展示所有数据源（prometheus / kafka / mongodb / mysql / redis /
 * elasticsearch），支持：
 *   - 列表（名称 / 类型 / 连接摘要 / 用途 / 启用开关 / 操作）
 *   - 新建（Dropdown 选类型，Prometheus 为内置单例，不可新建）
 *   - 编辑（右侧 Modal 内联表单，直接出字段，不再弹管理列表中间层）
 *   - 删除（原地 Popconfirm 确认即删）
 *   - 启用开关（toggleSource；内置 Prometheus 恒开且禁用）
 *   - 测试连接（按行类型组装 payload 调 testDataSource）
 *
 * 数据来源：listSources()（裸数组）。增删改 / 开关后刷新本地列表，并
 * dispatch(fetchSources()) 让侧边菜单按启用态动态更新。
 */
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useDispatch } from 'react-redux';
import { DownOutlined, ExportOutlined, ImportOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import {
  Alert,
  Button,
  Dropdown,
  Empty,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Popover,
  Radio,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import { getCustomPageLogoType,getSourceTypeMeta,SourceTypeLabel } from '@/components/SourceTypeLogo';
import { fetchSources } from '@/store/slices/sources';
import {
  createCustomPage,
  deleteCustomPage,
  deleteEsInstance,
  deleteHostInstance,
  deleteKafkaInstance,
  deleteMongoInstance,
  deleteMysqlInstance,
  deleteRedisInstance,
  exportSourcesUrl,
  getSourcesHealth,
  importTemplateUrl,
  listCustomPages,
  listSources,
  probeDataSource,
  testDataSource,
  toggleSource,
  updateCustomPage,
} from '@/utils/alertApi';
import { apiErr, keyText } from '@/utils/apiError';
import { getSourceDisplayName } from '@/utils/sourceDisplay';
import { _l } from '@/utils/utils';

import ImportModal from './ImportModal';
import SourceFormModal from './SourceFormModal';

import './index.less';

// type → 显示名 / Tag 颜色
const TYPE_META = {
  prometheus: { label: 'Prometheus', color: 'default' },
  host: { label: _l('主机'), color: 'default' },
  mysql: { label: 'MySQL', color: 'default' },
  mongodb: { label: 'MongoDB', color: 'default' },
  kafka: { label: 'Kafka', color: 'default' },
  redis: { label: 'Redis', color: 'default' },
  elasticsearch: { label: 'Elasticsearch', color: 'default' },
};

// 用途 → 标签
const PURPOSE_META = {
  metrics: { label: _l('看图'), color: 'default' },
  alert: { label: _l('告警'), color: 'default' },
  diagnose: { label: _l('慢查询诊断'), color: 'default' },
};

// 新建下拉菜单（不含内置单例 Prometheus）
const CREATE_TYPES = [
  { key: 'host', label: <SourceTypeLabel type="host" /> },
  { key: 'mysql', label: <SourceTypeLabel type="mysql" /> },
  { key: 'mongodb', label: <SourceTypeLabel type="mongodb" /> },
  { key: 'kafka', label: <SourceTypeLabel type="kafka" /> },
  { key: 'redis', label: <SourceTypeLabel type="redis" /> },
  { key: 'elasticsearch', label: <SourceTypeLabel type="elasticsearch" /> },
];

// 导出下拉：默认不带密码（导回同一套平台会沿用原密码），要真备份才选含密码那档
const EXPORT_MENU_ITEMS = [
  { key: 'plain', label: _l('导出配置（不含密码）') },
  { key: 'withSecrets', label: _l('导出配置（含明文密码）') },
];

// 从 detail 拼连接摘要
const connSummary = (record) => {
  const d = record.detail || {};
  switch (record.type) {
    case 'prometheus':
      return d.url || '-';
    case 'host':
      return d.address || '-';
    case 'kafka':
      return Array.isArray(d.brokers) ? d.brokers.join(', ') : (d.brokers || '-');
    case 'mongodb':
      return d.uri || '-';
    case 'mysql':
      return Array.isArray(d.nodes) ? d.nodes.join(', ') : (d.nodes || '-');
    case 'redis':
      return d.host ? `${d.host}:${d.port || 6379}` : '-';
    case 'elasticsearch':
      return d.url || '-';
    default:
      return '-';
  }
};

// 按行类型组装连接测试 payload
const buildTestPayload = (record) => {
  const d = record.detail || {};
  const base = { type: record.type, id: record.id, kind: record.kind };
  switch (record.type) {
    case 'prometheus':
      return { ...base, url: d.url, authType: d.authType, username: d.username, skipSSL: d.skipSSL };
    case 'host':
      return { ...base, address: d.address };
    case 'kafka':
      return { ...base, brokers: Array.isArray(d.brokers) ? d.brokers : [] };
    case 'mongodb':
      return { ...base, uri: d.uri };
    case 'mysql':
      return { ...base, nodes: Array.isArray(d.nodes) ? d.nodes : [] };
    case 'redis':
      return { ...base, host: d.host, port: d.port || 6379 };
    case 'elasticsearch':
      return { ...base, url: d.url };
    default:
      return base;
  }
};

// 按 type 调对应 delete API
const deleteByType = (type, id) => {
  switch (type) {
    case 'host': return deleteHostInstance(id);
    case 'kafka': return deleteKafkaInstance(id);
    case 'mongodb': return deleteMongoInstance(id);
    case 'mysql': return deleteMysqlInstance(id);
    case 'redis': return deleteRedisInstance(id);
    case 'elasticsearch': return deleteEsInstance(id);
    default: return Promise.reject(new Error('Unknown type'));
  }
};

// 不需要探测的行：内置 Prometheus(后端无此分支)，以及还没填地址的自定义页面(没什么可探的)。
const isProbeSkipped = (record) =>
  (record.builtin === true && record.type === 'prometheus') ||
  (record.__isCustom && !record.url);

// 从探测结果派生状态：'ok' | 'degraded' | 'error'
// 只看该源「已启用的用途」——没启用的用途(如单节点 mongo 的 alert)不可用属正常，不算降级。
const deriveProbeStatus = (result, enabledPurposes) => {
  if (!result || result.loading) return null;
  // 探测服务自己没响应 → 状态未知，不能显示成「连接失败」栽赃给用户的配置
  if (result.unavailable) return null;
  if (!result.connect?.ok) return 'error';
  const purposes = result.purposes || {};
  const enabled = Array.isArray(enabledPurposes) && enabledPurposes.length
    ? enabledPurposes
    : Object.keys(purposes);
  const hasDegraded = enabled.some((name) => purposes[name] && purposes[name].available === false);
  return hasDegraded ? 'degraded' : 'ok';
};

// 采集实况核验（后端 scrape 块）→ 一句话说明。
// 「连接成功」只说明 TCP 通 + 认证过，客户当场质疑过它没有意义——真正决定面板有没有图的是
// 「Prometheus 里此刻有没有这条源的指标」，所以测试结果里必须把这句一起说清楚。
const scrapeText = (scrape) => {
  if (!scrape || !scrape.state) return '';
  switch (scrape.state) {
    case 'ok':
      return _l('已在采集：Prometheus 里有这条数据源的指标');
    case 'noData':
    case 'down':
    case 'unknown':
      return (scrape.key && keyText(scrape.key)) || _l('采集情况未知');
    default:
      // skipped：这条源本就不参与采集（停用 / 没勾看图 / 该类型无采集概念）
      return (scrape.key && keyText(scrape.key)) || '';
  }
};

// 触发浏览器下载：导出接口靠 Content-Disposition 下发文件，
// 用临时 <a> 点击而不是 window.location，避免下载失败时把当前页面顶掉。
const triggerDownload = (url) => {
  const a = document.createElement('a');
  a.href = url;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
};

// 降级时取缺失用途说明（同样只列已启用且不可用的用途）
const getDegradedReasons = (result, enabledPurposes) => {
  if (!result || !result.purposes) return [];
  const purposes = result.purposes;
  const enabled = Array.isArray(enabledPurposes) && enabledPurposes.length
    ? enabledPurposes
    : Object.keys(purposes);
  return enabled
    .filter((name) => purposes[name] && purposes[name].available === false)
    .map((name) => {
      const cap = purposes[name];
      const text = (cap.reasonKey && keyText(cap.reasonKey)) || cap.reason || _l('不可用');
      return `${name}: ${text}`;
    });
};

const DataSources = () => {
  const dispatch = useDispatch();
  const [loading, setLoading] = useState(false);
  const [rows, setRows] = useState([]);
  const [togglingId, setTogglingId] = useState(null);
  const [testingId, setTestingId] = useState(null);
  // Modal 状态：{ open, type, record }
  const [sourceModal, setSourceModal] = useState({ open: false, type: null, record: null });
  // 探测结果缓存：id → { loading, connect, purposes } | null
  const [probeMap, setProbeMap] = useState({});
  // 防止组件卸载后 setState
  const mountedRef = useRef(true);
  useEffect(() => () => { mountedRef.current = false; }, []);

  // ── 自定义页面（合成列表行 + 配置 Modal）──────────────────────────────────
  // 1.5.12 前这里是写死的 Flink 单例；现在是任意多条外链页面（Flink 只是其中一条）。
  const [customPages, setCustomPages] = useState([]);
  const [customOpen, setCustomOpen] = useState(false);
  const [customSaving, setCustomSaving] = useState(false);
  // 正在编辑的那条（null=新建）
  const [customEditing, setCustomEditing] = useState(null);
  const [customTogglingId, setCustomTogglingId] = useState('');
  const [customDeletingId, setCustomDeletingId] = useState('');
  // 保存后端探测失败的原因：探不通仍然存，但要留在弹窗里让用户看见（见 handleCustomSave）
  const [customProbeError, setCustomProbeError] = useState('');
  const [customForm] = Form.useForm();

  const fetchCustomPages = useCallback(() => {
    listCustomPages()
      .then(({ data }) => {
        if (mountedRef.current) setCustomPages(Array.isArray(data) ? data : []);
      })
      .catch(() => {});
  }, []);

  const openCustomModal = (record) => {
    setCustomEditing(record || null);
    setCustomProbeError('');
    setCustomOpen(true);
    customForm.resetFields();
    customForm.setFieldsValue({
      name: record ? record.name : '',
      url: record ? record.url : '',
      mode: record ? record.mode : 'proxy',
    });
  };

  const closeCustomModal = () => {
    setCustomProbeError('');
    setCustomOpen(false);
  };

  const handleCustomSave = () => {
    customForm
      .validateFields()
      .then(({ name, url, mode }) => {
        setCustomSaving(true);
        const payload = {
          name: (name || '').trim(),
          url: (url || '').trim(),
          mode,
          enabled: customEditing ? customEditing.enabled : true,
        };
        const req = customEditing
          ? updateCustomPage(customEditing.id, payload)
          : createCustomPage(payload);
        return req
          .then(({ data }) => {
            // 后端探测不通时照存不误（目标还没起、隔离网段暂时探不到都是合法场景），
            // 但要把原因说清楚——零反馈的话，填错地址只表现为「菜单出现、点进去空白」。
            if (data && data.probeError) {
              setCustomProbeError(data.probeError);
              message.warning(_l('已保存，但地址探测未通过'));
            } else {
              setCustomProbeError('');
              message.success(_l('已保存'));
              setCustomOpen(false);
            }
            fetchCustomPages();
            // 配置变更影响左侧「自定义」菜单，刷新 redux 数据源态
            dispatch(fetchSources());
          })
          .catch((e) => message.error(apiErr(e, _l('保存失败'))))
          .finally(() => setCustomSaving(false));
      })
      .catch(() => {});
  };

  // 切换启用态（保留配置，仅控制菜单显隐）
  const handleCustomToggle = (record, checked) => {
    setCustomTogglingId(record.id);
    updateCustomPage(record.id, {
      name: record.name, url: record.url, mode: record.mode, enabled: checked,
    })
      .then(() => {
        message.success(checked ? _l('已启用') : _l('已停用'));
        fetchCustomPages();
        dispatch(fetchSources());
      })
      .catch((e) => message.error(apiErr(e, _l('操作失败'))))
      .finally(() => setCustomTogglingId(''));
  };

  const handleCustomDelete = (record) => {
    setCustomDeletingId(record.id);
    deleteCustomPage(record.id)
      .then(() => {
        message.success(_l('已删除'));
        fetchCustomPages();
        dispatch(fetchSources());
      })
      .catch((e) => message.error(apiErr(e, _l('删除失败'))))
      .finally(() => setCustomDeletingId(''));
  };

  // ── Data fetching ──────────────────────────────────────────────────────────

  const fetchList = useCallback(() => {
    setLoading(true);
    listSources()
      .then(({ data }) => setRows(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取数据源列表失败'))))
      .finally(() => setLoading(false));
  }, []);

  // 采集状态（agent 每 60s 上报一次，这里 30s 拉一次即可）
  const [health, setHealth] = useState({ reported: false, sources: {}, unmatched: [] });
  const fetchHealth = useCallback(() => {
    getSourcesHealth()
      .then(({ data }) => setHealth(data || { reported: false, sources: {}, unmatched: [] }))
      .catch(() => {});
  }, []);
  useEffect(() => {
    fetchHealth();
    const t = setInterval(fetchHealth, 30000);
    return () => clearInterval(t);
  }, [fetchHealth]);

  useEffect(() => {
    fetchList();
    fetchCustomPages();
  }, [fetchList, fetchCustomPages]);

  // 合成自定义页面行（追加到列表末尾，与其它数据源视觉统一）
  const customRows = useMemo(
    () => customPages.map((p) => ({ ...p, type: 'custom', __isCustom: true })),
    [customPages],
  );
  const tableData = useMemo(() => [...rows, ...customRows], [rows, customRows]);

  // 新建下拉项：常规可多实例类型 + 自定义页面（可加任意多条，不置灰）
  const createMenuItems = useMemo(
    () => [
      ...CREATE_TYPES,
      {
        key: 'custom',
        label: <SourceTypeLabel type="custom" />,
      },
    ],
    [],
  );

  // ── Probe (手动触发，避免大列表自动并发) ────────────────────────────────
  const [probing, setProbing] = useState(false);

  const runProbe = useCallback(async (targetRows) => {
    const probable = targetRows.filter((r) => !isProbeSkipped(r));
    if (!probable.length) return;
    setProbing(true);
    // 先把所有行标为 loading
    const loadingPatch = {};
    probable.forEach((r) => { loadingPatch[r.id] = { loading: true }; });
    setProbeMap((prev) => ({ ...prev, ...loadingPatch }));

    // 顺序探测，避免同时发起大量请求（每行等上一行完成再继续）
    for (const record of probable) {
      if (!mountedRef.current) break;
      try {
        const { data } = await probeDataSource({
          type: record.type,
          id: record.id,
          kind: record.kind,
          // 自定义页面是外链、不是 kind 数据源，后端按 url 探(缺省则按 id 读当前配置)
          ...(record.__isCustom ? { url: record.url } : null),
        });
        if (mountedRef.current) {
          setProbeMap((prev) => ({
            ...prev,
            [record.id]: { loading: false, ...(data || {}) },
          }));
        }
      } catch {
        if (mountedRef.current) {
          setProbeMap((prev) => ({
            ...prev,
            // 探测请求本身失败 ≠ 这条数据源连不上，别替用户下「你配错了」的结论
            [record.id]: { loading: false, unavailable: true },
          }));
        }
      }
    }
    if (mountedRef.current) setProbing(false);
  }, []);

  // rows 变化时清空旧探测结果（新行无状态）
  useEffect(() => {
    setProbeMap({});
  }, [rows]);

  // 增删改 / 开关后：刷新本地列表 + 刷新 redux（让菜单动态更新）
  const refreshAll = useCallback(() => {
    fetchList();
    dispatch(fetchSources());
  }, [fetchList, dispatch]);

  // 批量导入弹窗（YAML/JSON 模板）——1.5.0 起数据源的唯一批量入口
  const [importOpen, setImportOpen] = useState(false);

  // 导出配置（1.5.9）：产出的 YAML 就是「批量导入」吃的那份，重装/换环境能一次导回。
  // 含密码那档要二次确认——导出的是明文，落到桌面上就不归平台管了。
  const onExportMenuClick = ({ key }) => {
    if (key !== 'withSecrets') {
      triggerDownload(exportSourcesUrl());
      return;
    }
    Modal.confirm({
      title: _l('导出含明文密码的配置'),
      content: (
        <div>
          <div>{_l('文件里会包含各数据源的明文密码，用于换机器 / 换平台恢复。')}</div>
          <div style={{ marginTop: 6 }}>
            {_l('请妥善保管：别提交进代码仓库、别放在共享目录。')}
          </div>
          <div style={{ marginTop: 6, color: 'var(--color-text-secondary, #757575)' }}>
            {_l('导回同一套平台不需要密码——不含密码的那档留空即沿用平台里已存的密码。')}
          </div>
        </div>
      ),
      okText: _l('确认导出'),
      cancelText: _l('取消'),
      onOk: () => triggerDownload(exportSourcesUrl({ includeSecrets: true })),
    });
  };

  // ── Toggle enabled ───────────────────────────────────────────────────────

  const handleToggle = (record, checked) => {
    setTogglingId(record.id);
    toggleSource(record.kind, record.id, checked)
      .then(() => {
        message.success(checked ? _l('已启用') : _l('已停用'));
        refreshAll();
      })
      .catch((err) => {
        message.error(
          apiErr(
            err,
            record.builtin && record.type === 'prometheus'
              ? _l('内置数据源不可停用')
              : _l('操作失败，请重试'),
          ),
        );
        // 失败时刷新以回滚 Switch 视图
        fetchList();
      })
      .finally(() => setTogglingId(null));
  };

  // ── Test connection ──────────────────────────────────────────────────────

  const handleTest = (record) => {
    setTestingId(record.id);
    testDataSource(buildTestPayload(record))
      .then(({ data }) => {
        if (data && data.ok) {
          // 明确区分「连通性」和「实际采集」：这两条是不同链路，客户最容易在这里误判。
          // 后端顺带查了一次内置 Prometheus，所以这里能把「有没有真采到」一并说了，
          // 不用再让人自己去对「采集状态」列或翻看板。
          const st = data.scrape || {};
          const detail = scrapeText(st);
          if (st.state === 'ok') {
            message.success(_l('连接成功，%0', detail));
          } else if (st.state === 'noData' || st.state === 'down') {
            // 连得上但没数据：这才是客户真正想知道的那一半，用 warning 而不是 success
            message.warning(_l('连接成功，但还没采到数据：%0', detail), 8);
          } else if (detail) {
            message.success(_l('连接成功（%0）', detail));
          } else {
            message.success(_l('连接成功（此处只验证连通性，实际是否在采数据看「采集状态」列）'));
          }
        } else {
          message.error((data && data.key && apiErr({ response: { data } }, _l('连接失败'))) || _l('连接失败'));
        }
      })
      .catch((e) => message.error(apiErr(e, _l('连接测试请求失败'))))
      .finally(() => setTestingId(null));
  };

  // ── Modal open/close ────────────────────────────────────────────────────

  const onCreateMenuClick = ({ key }) => {
    // 自定义页面是外链（非常规数据源），走专属配置 Modal
    if (key === 'custom') {
      openCustomModal(null);
      return;
    }
    setSourceModal({ open: true, type: key, record: null });
  };

  const handleEdit = (record) =>
    setSourceModal({ open: true, type: record.type, record });

  const closeSourceModal = () =>
    setSourceModal({ open: false, type: null, record: null });

  // ── Delete (直接调 API + Popconfirm 原地确认) ────────────────────────────

  const handleDelete = (record) => {
    deleteByType(record.type, record.id)
      .then(() => {
        message.success(_l('已删除'));
        refreshAll();
      })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  // ── Columns ────────────────────────────────────────────────────────────────

  const columns = useMemo(
    () => [
      {
        title: _l('类型'),
        dataIndex: 'type',
        width: 160,
        render: (type, record) => {
          const meta = getSourceTypeMeta(type);
          // 自定义页面统一显示成「自定义页面」，图标则尽量用认得出的品牌图标
          // （迁移过来的 Flink 那条 id 就是 flink，图标跟着不变）。
          const logoType = record.__isCustom ? getCustomPageLogoType(record.id) : type;
          return <SourceTypeLabel type={logoType} label={meta.label} showText />;
        },
      },
      {
        title: _l('名称'),
        dataIndex: 'name',
        width: 170,
        ellipsis: true,
        render: (text, record) => (
          <span style={{ fontWeight: 500 }}>
            {getSourceDisplayName(record)}
            {record.builtin && (
              <Tag color="default" style={{ marginLeft: 8 }}>
                {_l('内置')}
              </Tag>
            )}
          </span>
        ),
      },
      {
        title: _l('连接摘要'),
        ellipsis: true,
        render: (_, record) => {
          if (record.__isCustom) {
            const url = (record.url || '').trim();
            if (!url) return <span className="dsEmpty">{_l('未配置')}</span>;
            // 反代模式额外说明平台内的访问路径 —— 用户要知道菜单点开后走的是哪条链路
            const summary = record.mode === 'iframe'
              ? _l('%0（直接嵌入）', url)
              : _l('%0（反代到 %1）', url, record.path || '');
            return (
              <Tooltip title={summary}>
                <span className="dsConnSummary">{summary}</span>
              </Tooltip>
            );
          }
          const text = connSummary(record);
          return (
            <Tooltip title={text}>
              <span className="dsConnSummary">
                {text}
              </span>
            </Tooltip>
          );
        },
      },
      {
        // 采集状态：agent 上报的 exporter 实况。红点 hover 出失败原因原文，
        // 用户不用再去猜该进哪个容器看哪个日志——这是真实客户部署反馈的头号痛点。
        title: _l('采集状态'),
        dataIndex: 'id',
        key: 'collectStatus',
        width: 120,
        render: (id, record) => {
          const usesMetrics = Array.isArray(record.purposes) && record.purposes.includes('metrics');
          if (!usesMetrics) return <span className="dsEmpty">-</span>;
          if (!health.reported) {
            return (
              <Tooltip title={_l('ops-agent 尚未上报采集状态，请确认 agent 容器已启动')}>
                <Tag color="default">{_l('未知')}</Tag>
              </Tooltip>
            );
          }
          const st = health.sources && health.sources[id];
          if (!st) {
            // 原文案后半句是「若当前是环境变量模式，此页配置不参与采集」——那是 1.4.x 的说法，
            // 1.5.0 已删掉整套 ENV 播种机制，该模式不复存在，留着只会把人往错方向带。
            //
            // 主机类型走的是另一条路（后端查 Prometheus，不经 agent），到这里只可能是那次查询
            // 失败了。给它套 agent 的文案会把人送去看一个跟主机采集毫无关系的容器日志。
            if (record.type === 'host') {
              return (
                <Tooltip title={_l('暂时取不到采集状态：后端读内置 Prometheus 失败，确认 ops-prometheus 容器正常后刷新重试')}>
                  <Tag color="default">{_l('未知')}</Tag>
                </Tooltip>
              );
            }
            return (
              <Tooltip title={_l('agent 尚未为此数据源启动采集进程。新建或修改数据源后有几十秒的重建延迟，可稍后刷新；若持续如此，查看 ops-agent 容器日志')}>
                <Tag color="default">{_l('未采集')}</Tag>
              </Tooltip>
            );
          }
          if (st.collecting) return <Tag color="success">{_l('采集中')}</Tag>;
          // reason 有两个来源：ops-agent 上报的是 exporter 日志原文（底层文本，原样展示），
          // 后端自己判定的走 reasonKey（英文机器键，文案归前端，见 CLAUDE.md 的约定）。
          const reasonText = (st.reasonKey && keyText(st.reasonKey)) || st.reason || _l('采集失败');
          // 主机的 node_exporter 跑在被监控主机上，不在 agent 容器里——给 agent 容器的
          // 日志/端口命令等于把人指到一个根本不相关的地方，所以这一类换成探自己的地址。
          const isHost = st.type === 'host';
          const cmds = isHost
            ? [`curl -s http://${st.target}/metrics | head -20`]
            : [
                `docker logs --tail 50 $(docker ps -qf name=agent)`,
                `docker exec -it $(docker ps -qf name=agent) sh -c "curl -s http://127.0.0.1:${st.port}/metrics | head -20"`,
              ];
          return (
            <Popover
              placement="left"
              title={_l('采集未成功')}
              content={
                <div style={{ maxWidth: 460 }}>
                  <div style={{ marginBottom: 10, wordBreak: 'break-all' }}>{reasonText}</div>
                  <div style={{ color: 'var(--color-text-secondary, #757575)', marginBottom: 4 }}>
                    {isHost
                      ? _l('在能访问该主机的机器上执行以下命令可看到更多现场信息：')
                      : _l('在部署机上执行以下命令可看到更多现场信息：')}
                  </div>
                  {cmds.map((c, i) => (
                    <Typography.Paragraph
                      key={c}
                      copyable
                      code
                      style={{ marginBottom: i === cmds.length - 1 ? 0 : 4 }}
                    >
                      {c}
                    </Typography.Paragraph>
                  ))}
                </div>
              }
            >
              <Tag color="error" style={{ cursor: 'pointer' }}>
                {st.running ? _l('无数据') : _l('已停止')}
              </Tag>
            </Popover>
          );
        },
      },
      {
        title: _l('用途'),
        dataIndex: 'purposes',
        width: 220,
        render: (purposes) => {
          const list = Array.isArray(purposes) ? purposes : [];
          if (!list.length) return <span className="dsEmpty">-</span>;
          return (
            <Space size={4} className="dsPurposeTags">
              {list.map((p) => {
                const meta = PURPOSE_META[p] || { label: p, color: 'default' };
                return (
                  <Tag key={p} color={meta.color}>
                    {meta.label}
                  </Tag>
                );
              })}
            </Space>
          );
        },
      },
      {
        title: _l('启用'),
        dataIndex: 'enabled',
        width: 72,
        align: 'center',
        render: (enabled, record) => {
          if (record.__isCustom) {
            return (
              <Switch
                size="small"
                checked={!!record.enabled}
                loading={customTogglingId === record.id}
                onChange={(checked) => handleCustomToggle(record, checked)}
              />
            );
          }
          const isBuiltinProm = record.builtin && record.type === 'prometheus';
          return (
            <Switch
              size="small"
              checked={isBuiltinProm ? true : !!enabled}
              disabled={isBuiltinProm}
              loading={togglingId === record.id}
              onChange={(checked) => handleToggle(record, checked)}
            />
          );
        },
      },
      {
        title: _l('状态'),
        dataIndex: 'id',
        width: 84,
        render: (id, record) => {
          if (isProbeSkipped(record)) {
            return <span className="dsEmpty">—</span>;
          }
          const probe = probeMap[id];
          if (!probe) {
            return <span className="dsEmpty">—</span>;
          }
          if (probe.loading) {
            return (
              <Spin size="small" />
            );
          }
          const status = deriveProbeStatus(probe, record.purposes);
          if (status === 'error') {
            return (
              <Tooltip title={probe.connect?.message || _l('连接失败')}>
                <Tag color="error">{_l('不通')}</Tag>
              </Tooltip>
            );
          }
          // 连得上但 Prometheus 里没这条源的数据 —— 比「部分用途不可用」更该先说，
          // 因为它直接对应「面板是空的」这个用户唯一在意的现象。
          const scrape = probe.scrape || {};
          if (scrape.state === 'noData' || scrape.state === 'down') {
            return (
              <Tooltip title={scrapeText(scrape)}>
                <Tag color="warning">{_l('无数据')}</Tag>
              </Tooltip>
            );
          }
          if (status === 'degraded') {
            const reasons = getDegradedReasons(probe, record.purposes);
            return (
              <Tooltip title={reasons.join('\n') || _l('部分用途不可用')}>
                <Tag color="warning">{_l('降级')}</Tag>
              </Tooltip>
            );
          }
          if (scrape.state === 'ok') {
            return (
              <Tooltip title={scrapeText(scrape)}>
                <Tag color="success">{_l('连通·采集中')}</Tag>
              </Tooltip>
            );
          }
          return <Tag color="success">{_l('连通')}</Tag>;
        },
      },
      {
        title: _l('操作'),
        width: 110,
        fixed: 'right',
        render: (_, record) => {
          // 自定义页面行：编辑(改名称/地址/嵌法) + 删除，无「测试连接」(状态列已经在探它了)
          if (record.__isCustom) {
            return (
              <span className="opsActionGroup">
                <ActionIcon type="edit" title={_l('编辑')} onClick={() => openCustomModal(record)} />
                <Popconfirm
                  title={_l('确认删除')}
                  description={_l('删除后左侧「自定义」里这个入口将消失，确认继续？')}
                  okText={_l('删除')}
                  okType="danger"
                  cancelText={_l('取消')}
                  onConfirm={() => handleCustomDelete(record)}
                >
                  <span>
                    <ActionIcon type="delete" title={_l('删除')} danger loading={customDeletingId === record.id} />
                  </span>
                </Popconfirm>
              </span>
            );
          }
          return (
          <span className="opsActionGroup">
            {/* 多数类型(含内置 Prometheus / host)后端支持测试连接；K8s 集群仅注册抓取目标无连通测试 */}

              <ActionIcon
                type="test"
                title={testingId === record.id ? _l('测试中…') : _l('测试连接')}
                onClick={() => handleTest(record)}
                loading={testingId === record.id}
              />

            {!record.builtin && (
              <>
                <ActionIcon type="edit" title={_l('编辑')} onClick={() => handleEdit(record)} />
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
              </>
            )}
          </span>
          );
        },
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [togglingId, testingId, probeMap, customTogglingId, customDeletingId]
  );

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="dataSourcesContainer">
      {health.reported && health.unmatched && health.unmatched.length > 0 && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={_l('有 %0 个采集进程对应不上任何数据源', health.unmatched.length)}
          description={
            <div>
              <div style={{ marginBottom: 4 }}>
                {_l('通常是刚删掉数据源、采集进程还没回收，30 秒内会自动清理；若持续存在请反馈。')}
              </div>
              {health.unmatched.map((u) => (
                <div key={`${u.type}-${u.port}`}>
                  {u.type} · {u.target} ·{' '}
                  {u.collecting ? _l('采集中') : `${u.running ? _l('无数据') : _l('已停止')}${u.reason ? `（${u.reason}）` : ''}`}
                </div>
              ))}
            </div>
          }
        />
      )}
      {/* ── Toolbar ─────────────────────────────────────────────────── */}
      <div className="dsToolbar opsPageHeader">
        <div className="opsPageTitle">{_l('数据源')}</div>
        <Space>
          <Button
            icon={<ReloadOutlined />}
            loading={probing}
            disabled={loading}
            onClick={() => runProbe(tableData)}
          >
            {_l('检测连通性')}
          </Button>
          <Button icon={<ImportOutlined />} onClick={() => setImportOpen(true)}>
            {_l('批量导入')}
          </Button>
          <Dropdown
            menu={{ items: EXPORT_MENU_ITEMS, onClick: onExportMenuClick }}
            trigger={['click']}
          >
            <Button icon={<ExportOutlined />} disabled={loading || rows.length === 0}>
              {_l('导出配置')} <DownOutlined />
            </Button>
          </Dropdown>
          <Dropdown
            menu={{ items: createMenuItems, onClick: onCreateMenuClick }}
            trigger={['click']}
          >
            <Button type="primary" icon={<PlusOutlined />}>
              {_l('新建数据源')} <DownOutlined />
            </Button>
          </Dropdown>
        </Space>
      </div>

      {/* ── Table ───────────────────────────────────────────────────── */}
      <div className="dsTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={tableData}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false }}
          scroll={{ x: 950 }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  <div>
                    <div>{_l('还没有数据源')}</div>
                    <div style={{ marginTop: 4 }}>
                      {_l('单个添加点右上角「新建数据源」；要一次接入多个，先下载模板再批量导入。')}
                    </div>
                    <Space style={{ marginTop: 12 }}>
                      <Button size="small" onClick={() => setImportOpen(true)}>
                        {_l('批量导入')}
                      </Button>
                      <Button size="small" type="link" href={importTemplateUrl()} download>
                        {_l('下载 YAML 模板')}
                      </Button>
                    </Space>
                  </div>
                }
              />
            ),
          }}
        />
      </div>

      <ImportModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImported={refreshAll}
      />

      {/* ── Modal 内联表单（新建 / 编辑，砍掉中间层管理弹窗）──────── */}
      <SourceFormModal
        open={sourceModal.open}
        type={sourceModal.type}
        record={sourceModal.record}
        onClose={closeSourceModal}
        onSaved={refreshAll}
        existingSources={rows}
        onEditOther={(dup) => {
          // 「去补用途」：关掉当前新建弹窗，直接打开那条已有数据源的编辑框
          const target = rows.find((r) => r.id === dup.id);
          if (target) setSourceModal({ open: true, type: target.type, record: target });
        }}
      />

      {/* ── 自定义页面配置 Modal ────────────────────────────────────── */}
      <Modal
        title={customEditing ? _l('编辑自定义页面') : _l('新增自定义页面')}
        open={customOpen}
        onOk={handleCustomSave}
        onCancel={closeCustomModal}
        okText={customProbeError ? _l('仍然保存') : _l('保存')}
        cancelText={_l('取消')}
        confirmLoading={customSaving}
        destroyOnClose
      >
        {customProbeError && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message={_l('地址已保存，但探测没通过')}
            description={
              <>
                <div>{customProbeError}</div>
                <div style={{ marginTop: 6, color: '#8c8c8c' }}>
                  {_l('若目标服务尚未启动或网络暂时不通，可以先这样放着——通了菜单里就能正常打开。')}
                </div>
              </>
            }
          />
        )}
        <Form form={customForm} layout="vertical" preserve={false} initialValues={{ mode: 'proxy' }}>
          <Form.Item
            name="name"
            label={_l('名称')}
            extra={_l('显示在左侧「自定义」菜单里。访问路径由名称自动生成，建后不再变。')}
            rules={[{ required: true, message: _l('请输入名称') }]}
          >
            <Input placeholder="Langfuse" allowClear />
          </Form.Item>
          <Form.Item
            name="url"
            label={_l('页面地址')}
            rules={[
              {
                validator: (_rule, value) =>
                  /^https?:\/\/.+/.test((value || '').trim())
                    ? Promise.resolve()
                    : Promise.reject(new Error(_l('请输入有效的 http(s) 地址'))),
              },
            ]}
          >
            <Input placeholder="http://langfuse:3000" allowClear />
          </Form.Item>
          <Form.Item
            name="mode"
            label={_l('嵌入方式')}
            extra={_l('反代：经平台转发，同源、带平台鉴权，适合资源用相对路径的服务（如 Flink）。直接嵌入：浏览器直连目标地址，要求目标允许被 iframe 嵌入；资源用绝对路径的应用（如 Langfuse 这类 Next.js 服务）只能用这种。')}
          >
            <Radio.Group
              options={[
                { label: _l('反代'), value: 'proxy' },
                { label: _l('直接嵌入'), value: 'iframe' },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default DataSources;
