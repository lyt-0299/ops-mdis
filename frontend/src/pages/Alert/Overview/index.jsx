import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import {
  ClockCircleOutlined,
  DatabaseFilled,
  SyncOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import { Alert, Spin } from 'antd';

import SourceTypeLogo, { getSourceTypeMeta } from '@/components/SourceTypeLogo';
import {
  getSelfCheck,
  listAlertRules,
  listGlobalAlertHistory,
  listKafkaRebalanceMonitors,
  listMongoMonitors,
  listMysqlMonitors,
  listPortMonitors,
  listSources,
  listSslMonitors,
  listTasks,
} from '@/utils/alertApi';
import { _l } from '@/utils/utils';

import './index.less';

const REFRESH_INTERVAL = 30000;
const HISTORY_PAGE_SIZE = 200;

const MONITOR_CONFIGS = [
  {
    key: 'infra',
    label: _l('基础资源监控'),
    path: '/alert/infra-monitor',
    fetchFn: () => listAlertRules(),
    icon: <OpsIcon type="server" />,
  },
  {
    key: 'port',
    label: _l('端口连通性'),
    path: '/alert/port',
    fetchFn: () => listPortMonitors(),
    icon: <OpsIcon type="port" />,
  },
  {
    key: 'ssl',
    label: _l('SSL 证书'),
    path: '/alert/ssl',
    fetchFn: () => listSslMonitors(),
    icon: <OpsIcon type="shield" />,
  },
  {
    key: 'mysql',
    label: 'MySQL',
    path: '/alert/mysql',
    fetchFn: () => listMysqlMonitors(),
    icon: <OpsIcon type="database" />,
  },
  {
    key: 'mongo',
    label: 'MongoDB',
    path: '/alert/mongo',
    fetchFn: () => listMongoMonitors(),
    icon: <OpsIcon type="leaf" />,
  },
  {
    key: 'kafka',
    label: _l('Kafka 堆积'),
    path: '/alert/kafka-lag',
    fetchFn: () => listTasks(),
    icon: <OpsIcon type="queue" />,
  },
  {
    key: 'kafkaRb',
    label: _l('Kafka 重平衡'),
    path: '/alert/kafka-rebalance',
    fetchFn: () => listKafkaRebalanceMonitors(),
    icon: <OpsIcon type="rebalance" />,
  },
];

// 类型显示名统一取自共享的 SourceTypeLogo（与数据源列表同一来源，避免命名漂移）。
const SOURCE_TYPE_ORDER = [
  'prometheus',
  'host',
  'mysql',
  'mongodb',
  'kafka',
  'redis',
  'elasticsearch',
];

function normalizeSourceType(type) {
  const value = type || 'prometheus';
  if (value === 'mongo') return 'mongodb';
  if (value === 'elastic') return 'elasticsearch';
  return value;
}

function countItems(items) {
  const active = items.filter((i) => i.status === 'active');
  return {
    total: items.length,
    active: active.length,
    paused: items.filter((i) => i.status === 'paused').length,
    firing: active.filter((i) => i.alertState === 'Firing').length,
    pending: active.filter((i) => i.alertState === 'Pending').length,
    ok: active.filter(
      (i) => i.alertState === 'OK' || i.alertState === 'Recovered',
    ).length,
  };
}

function getHealthRate(global) {
  const activeTotal = global.ok + global.firing + global.pending;
  // 一条启用中的规则都没有时，「健康度」无从谈起。此前这里直接 return 100，
  // 于是刚装完、什么都没配的实例也显示「100% · 良好 · 整体运行状态良好」，
  // 把「没在监控」说成了「一切正常」。null = 不可评，由 HealthCard 渲染成「—」。
  if (activeTotal <= 0) return null;
  return Math.round((global.ok / activeTotal) * 100);
}

function formatTime(isoStr) {
  if (!isoStr) return '-';
  const d = new Date(isoStr);
  if (Number.isNaN(d.getTime())) return isoStr;
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

function getHistoryTime(item) {
  return item?.triggeredAt || item?.createdAt || item?.updatedAt || item?.time;
}

const Overview = () => {
  const navigate = useNavigate();
  const [countsMap, setCountsMap] = useState({});
  const [dataSources, setDataSources] = useState([]);
  const [firingItems, setFiringItems] = useState([]);
  const [historyItems, setHistoryItems] = useState([]);
  const [firstLoad, setFirstLoad] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [lastUpdated, setLastUpdated] = useState(null);
  // 部署自检结果（后端一次性汇总各链路状态，见 GET /alert/selfcheck）
  const [selfCheck, setSelfCheck] = useState(null);
  const [countdown, setCountdown] = useState(REFRESH_INTERVAL / 1000);
  const nextRefreshAt = useRef(Date.now() + REFRESH_INTERVAL);

  const fetchAll = useCallback(async () => {
    setRefreshing(true);

    const endTime = new Date();
    const startTime = new Date(endTime.getTime() - 24 * 60 * 60 * 1000);

    const [monitorResults, historyResult, dataSourceResult] = await Promise.all([
      Promise.allSettled(MONITOR_CONFIGS.map((c) => c.fetchFn())),
      listGlobalAlertHistory({
        page: 1,
        pageSize: HISTORY_PAGE_SIZE,
        sort: 'desc',
        startTime: startTime.toISOString(),
        endTime: endTime.toISOString(),
      }).catch(() => null),
      listSources().catch(() => null),
    ]);

    const newCounts = {};
    const newFiring = [];

    MONITOR_CONFIGS.forEach((cfg, idx) => {
      const result = monitorResults[idx];
      if (result.status === 'fulfilled' && result.value?.data) {
        const items = Array.isArray(result.value.data) ? result.value.data : [];
        newCounts[cfg.key] = countItems(items);
        items
          .filter((i) => i.status === 'active' && i.alertState === 'Firing')
          .forEach((i) => {
            newFiring.push({
              monitorLabel: cfg.label,
              path: cfg.path,
              itemName: i.name,
              firingAt: i.firingAt || i.lastCheckAt || i.updatedAt,
            });
          });
      } else {
        newCounts[cfg.key] = null;
      }
    });

    const histories = historyResult?.data?.items || historyResult?.data || [];
    const sources = dataSourceResult?.data || [];

    setCountsMap(newCounts);
    setFiringItems(newFiring);
    setHistoryItems(Array.isArray(histories) ? histories : []);
    setDataSources(Array.isArray(sources) ? sources : []);
    setLastUpdated(new Date());
    nextRefreshAt.current = Date.now() + REFRESH_INTERVAL;
    setCountdown(REFRESH_INTERVAL / 1000);
    setFirstLoad(false);
    setRefreshing(false);
  }, []);

  // 自检结果单独拉取：后端会去探 Loki / Tempo，比概览主数据慢，不能拖住主流程
  useEffect(() => {
    const load = () => {
      getSelfCheck()
        .then(({ data }) => setSelfCheck(data))
        .catch(() => setSelfCheck(null));
    };
    load();
    const t = setInterval(load, 60000);
    return () => clearInterval(t);
  }, []);

  useEffect(() => {
    fetchAll();
    const timer = setInterval(() => fetchAll(), REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [fetchAll]);

  useEffect(() => {
    const tick = setInterval(() => {
      setCountdown(
        Math.max(1, Math.ceil((nextRefreshAt.current - Date.now()) / 1000)),
      );
    }, 1000);
    return () => clearInterval(tick);
  }, []);

  const global = useMemo(
    () =>
      Object.values(countsMap).reduce(
        (acc, c) => {
          if (!c) return acc;
          acc.total += c.total;
          acc.ok += c.ok;
          acc.firing += c.firing;
          acc.pending += c.pending;
          acc.paused += c.paused;
          return acc;
        },
        { total: 0, ok: 0, firing: 0, pending: 0, paused: 0 },
      ),
    [countsMap],
  );

  const healthRate = getHealthRate(global);
  const moduleStats = MONITOR_CONFIGS.map((cfg) => {
    const counts = countsMap[cfg.key] || {
      total: 0,
      ok: 0,
      firing: 0,
      pending: 0,
      paused: 0,
    };
    // 同 getHealthRate：这一类没有启用中的规则就不给分，null 渲染成「—」
    const rate = counts.active > 0 ? Math.round((counts.ok / counts.active) * 100) : null;
    return { ...cfg, counts, rate };
  });

  const sourceSummary = useMemo(() => {
    const grouped = dataSources.reduce((acc, source) => {
      const type = normalizeSourceType(source.type);
      acc[type] = (acc[type] || 0) + 1;
      return acc;
    }, {});
    const types = Object.keys(grouped).sort((a, b) => {
      const aIndex = SOURCE_TYPE_ORDER.indexOf(a);
      const bIndex = SOURCE_TYPE_ORDER.indexOf(b);
      return (aIndex === -1 ? 99 : aIndex) - (bIndex === -1 ? 99 : bIndex);
    });
    return [
      { type: 'all', label: _l('全部'), value: dataSources.length },
      ...types.map((type) => ({
        type,
        label: getSourceTypeMeta(type).label,
        value: grouped[type],
      })),
    ];
  }, [dataSources]);

  if (firstLoad) {
    return (
      <div className="overviewLoading">
        <Spin size="large" />
      </div>
    );
  }

  return (
    <div className="alertOverviewContainer">
      <div className="ovHeader opsPageHeader">
        <div className="opsPageTitle">{_l('概览')}</div>
        <div className="ovHeaderRight">
          <div className="ovSubtitle">
            {lastUpdated
              ? _l('数据更新时间：%0', formatTime(lastUpdated.toISOString()))
              : _l('获取中...')}
            <span className="ovSubDivider">|</span>
            {_l('%0 秒后刷新', countdown)}
          </div>
          <button
            className={`ovRefreshBtn${refreshing ? ' ovRefreshBtn--spinning' : ''}`}
            onClick={() => fetchAll()}
            disabled={refreshing}
            type="button"
          >
            <SyncOutlined spin={refreshing} />
            {_l('立即刷新')}
          </button>
        </div>
      </div>

      {/* 部署自检：装完/出问题时先看这里。只在有异常项时展开显示，正常时收成一行。 */}
      {selfCheck && selfCheck.items && selfCheck.items.some((i) => i.status !== 'ok') && (
        <Alert
          type={selfCheck.items.some((i) => i.status === 'error') ? 'error' : 'warning'}
          showIcon
          style={{ marginBottom: 16 }}
          message={_l('部署自检发现 %0 项需要处理', selfCheck.items.filter((i) => i.status !== 'ok').length)}
          description={
            <div>
              {selfCheck.items
                .filter((i) => i.status !== 'ok')
                .map((i) => (
                  <div key={i.key} style={{ marginBottom: 6 }}>
                    <strong>{i.title}</strong>：{i.detail}
                    {i.hint && (
                      <div style={{ color: 'var(--color-text-secondary, #757575)' }}>{i.hint}</div>
                    )}
                  </div>
                ))}
            </div>
          }
        />
      )}

      <div className="overviewTopGrid">
        <HealthCard rate={healthRate} />
        <KpiCard label={_l('总监控任务')} value={global.total} icon={<OpsIcon type="monitor" />} />
        <KpiCard label={_l('运行正常')} value={global.ok} variant="ok" icon={<OpsIcon type="check" />} />
        <KpiCard label={_l('告警中')} value={global.firing} variant="danger" icon={<OpsIcon type="bell" />} />
        <KpiCard label={_l('预警中')} value={global.pending} variant="warning" icon={<OpsIcon type="bolt" />} />
      </div>

      <div className="overviewMainGrid">
        <Panel
          title={_l('当前活跃告警')}
          icon={<WarningOutlined />}
          badge={firingItems.length || null}
          tone="danger"
        >
          {firingItems.length === 0 ? (
            <EmptyBlock text={_l('当前暂无活跃告警')} />
          ) : (
            firingItems.slice(0, 5).map((item, idx) => (
              <button
                key={`${item.itemName}-${idx}`}
                className="activeAlertItem"
                onClick={() => navigate(item.path)}
                type="button"
              >
                <span className={`activeAlertItem__level activeAlertItem__level--${idx === 0 ? 'high' : 'medium'}`}>
                  {idx === 0 ? _l('高') : _l('中')}
                </span>
                <span className="activeAlertItem__info">
                  <strong>{item.itemName}</strong>
                  <em>{item.monitorLabel}</em>
                </span>
                <span className="activeAlertItem__time">{formatTime(item.firingAt)}</span>
              </button>
            ))
          )}
          {firingItems.length > 0 && (
            <button className="panelLinkBtn" onClick={() => navigate('/alert/history')} type="button">
              {_l('查看全部告警')}
            </button>
          )}
        </Panel>

        <Panel
          title={_l('近 24 小时告警动态')}
          icon={<ClockCircleOutlined />}
          subTitle={_l('基于告警历史')}
        >
          <AlertTrendChart items={historyItems} />
        </Panel>
      </div>

      <div className="overviewSecondaryGrid">
        <Panel title={_l('资源状态')} action={_l('查看更多')} onAction={() => navigate('/alert/infra-monitor')}>
          <div className="resourceStatusList">
            {moduleStats.slice(0, 6).map((item) => {
              const { firing, pending, active } = item.counts;
              // 状态文案取严重度最高的一档。此前只判 firing，于是「预警中」被写成
              // 「正常」，而右边比率又是 0%（ok/active = 0），同一行自相矛盾；
              // 一条规则都没有时同样显示「正常」，把没配当成了健康。
              const stateText = firing > 0
                ? _l('%0 告警', firing)
                : pending > 0
                  ? _l('%0 预警', pending)
                  : active > 0 ? _l('正常') : _l('未配置');
              const stateTone = firing > 0
                ? ' isAlert'
                : pending > 0 ? ' isPending' : active > 0 ? '' : ' isIdle';
              return (
                <button
                  key={item.key}
                  className="resourceStatusItem"
                  onClick={() => navigate(item.path)}
                  type="button"
                >
                  <span className="opsUnifiedIcon">{item.icon}</span>
                  <span className="resourceStatusItem__name">{item.label}</span>
                  <span className={`resourceStatusItem__state${stateTone}`}>{stateText}</span>
                  <span className="resourceStatusItem__bar">
                    <i style={{ width: `${item.rate === null ? 0 : item.rate}%` }} />
                  </span>
                  <span className="resourceStatusItem__rate">
                    {item.rate === null ? '—' : `${item.rate}%`}
                  </span>
                </button>
              );
            })}
          </div>
        </Panel>

        <Panel title={_l('数据源概览')} action={_l('管理数据源')} onAction={() => navigate('/datasources')}>
          <div className="sourceSummaryGrid">
            {sourceSummary.map((item) => (
              <div key={item.type} className="sourceSummaryItem">
                <span className="opsUnifiedIcon">
                  {item.type === 'all' ? <DatabaseFilled /> : <SourceTypeLogo type={item.type} />}
                </span>
                <span className="sourceSummaryItem__content">
                  <span className="sourceSummaryItem__label">{item.label}</span>
                  <strong>{item.value}</strong>
                </span>
              </div>
            ))}
          </div>
        </Panel>
      </div>
    </div>
  );
};

function HealthCard({ rate }) {
  // rate === null：一条启用中的规则都没有，不是「满分」而是「没在看」
  const idle = rate === null || rate === undefined;
  const tone = idle ? 'idle' : rate >= 90 ? 'good' : rate >= 70 ? 'warning' : 'danger';
  const statusText = idle
    ? _l('未配置')
    : rate >= 90 ? _l('良好') : rate >= 70 ? _l('关注') : _l('异常');
  const description = idle
    ? _l('还没有启用任何监控规则')
    : rate >= 90 ? _l('整体运行状态良好') : _l('存在需要处理的风险');
  return (
    <div className={`healthHeroCard healthHeroCard--${tone}`}>
      <div className="healthRingChart" style={{ '--health-rate': idle ? 0 : rate }}>
        <svg viewBox="0 0 120 120" aria-hidden="true">
          <circle className="healthRingChart__track" cx="60" cy="60" r="45" />
          <circle className="healthRingChart__glow" cx="60" cy="60" r="45" />
          <circle className="healthRingChart__bar" cx="60" cy="60" r="45" />
        </svg>
        <div className="healthRingChart__value">
          <strong>{idle ? '—' : rate}</strong>
          {!idle && <em>%</em>}
        </div>
      </div>
      <div className="healthHeroCard__meta">
        <div>{_l('系统健康度')}</div>
        <span>{statusText}</span>
        <p>{description}</p>
      </div>
    </div>
  );
}

function KpiCard({ label, value, variant = 'default', icon }) {
  return (
    <div className={`ovKpiCard ovKpiCard--${variant}`}>
      <span className="ovKpiCard__icon">{icon}</span>
      <div>
        <span className="ovKpiCard__label">{label}</span>
        <strong>{value}</strong>
      </div>
    </div>
  );
}

function OpsIcon({ type }) {
  const common = {
    width: 22,
    height: 22,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 2,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
  };

  switch (type) {
    case 'server':
    case 'monitor':
      return (
        <svg {...common}>
          <rect x="4" y="4" width="16" height="6" rx="1.6" />
          <rect x="4" y="14" width="16" height="6" rx="1.6" />
          <path d="M8 7h.01M8 17h.01M12 7h4M12 17h4" />
        </svg>
      );
    case 'port':
      return (
        <svg {...common}>
          <rect x="4" y="15" width="5" height="5" rx="1.2" />
          <rect x="15" y="15" width="5" height="5" rx="1.2" />
          <rect x="9.5" y="4" width="5" height="5" rx="1.2" />
          <path d="M12 9v3.5M6.5 15v-2.5h11V15" />
        </svg>
      );
    case 'shield':
      return (
        <svg {...common}>
          <path d="M12 3l7 3v5.4c0 4.8-3 7.8-7 9.6-4-1.8-7-4.8-7-9.6V6l7-3z" />
          <path d="M9 12l2 2 4-5" />
        </svg>
      );
    case 'database':
      return (
        <svg {...common}>
          <ellipse cx="12" cy="5.5" rx="7.5" ry="3" />
          <path d="M4.5 5.5v12c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-12" />
          <path d="M4.5 11.5c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3" />
        </svg>
      );
    case 'leaf':
      return (
        <svg {...common}>
          <path d="M12 21c4.2-2.4 6.5-5.6 6.5-9.4 0-3.3-1.8-6-6.5-8.6-4.7 2.6-6.5 5.3-6.5 8.6C5.5 15.4 7.8 18.6 12 21z" />
          <path d="M12 8v13" />
        </svg>
      );
    case 'queue':
      return (
        <svg {...common}>
          <path d="M5 7h14M5 12h14M5 17h14" />
          <path d="M8 7v10M16 7v10" />
        </svg>
      );
    case 'rebalance':
      return (
        <svg {...common}>
          <path d="M18 7h2v5h-5" />
          <path d="M6 17H4v-5h5" />
          <path d="M19.5 12A7.5 7.5 0 0 0 6.2 7.4L4 12M4.5 12A7.5 7.5 0 0 0 17.8 16.6L20 12" />
        </svg>
      );
    case 'check':
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="8" />
          <path d="M8.5 12.2l2.2 2.2 4.8-5" />
        </svg>
      );
    case 'bell':
      return (
        <svg {...common}>
          <path d="M18 16H6l1.4-1.8V10a4.6 4.6 0 0 1 9.2 0v4.2L18 16z" />
          <path d="M10 18a2 2 0 0 0 4 0" />
        </svg>
      );
    case 'bolt':
      return (
        <svg {...common}>
          <path d="M13 2L5 13h6l-1 9 9-13h-6l1-7z" />
        </svg>
      );
    default:
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="8" />
        </svg>
      );
  }
}

function Panel({ title, icon, subTitle, badge, action, onAction, tone, children }) {
  return (
    <section className={`dashboardPanel${tone ? ` dashboardPanel--${tone}` : ''}`}>
      <div className="dashboardPanel__header">
        <div className="dashboardPanel__title">
          {icon && <span>{icon}</span>}
          <strong>{title}</strong>
          {subTitle && <em>{subTitle}</em>}
        </div>
        {badge ? <i className="dashboardPanel__badge">{badge}</i> : null}
        {action ? (
          <button className="dashboardPanel__action" onClick={onAction} type="button">
            {action}
          </button>
        ) : null}
      </div>
      <div className="dashboardPanel__body">{children}</div>
    </section>
  );
}

function EmptyBlock({ text }) {
  return <div className="ovPanelEmpty">{text}</div>;
}

function AlertTrendChart({ items }) {
  const chart = useMemo(() => {
    const bucketCount = 12;
    const now = Date.now();
    const start = now - 24 * 60 * 60 * 1000;
    const bucketMs = (now - start) / bucketCount;
    const buckets = Array.from({ length: bucketCount }, (_, index) => ({
      index,
      firing: 0,
      recovered: 0,
    }));

    items.forEach((item) => {
      const ts = new Date(getHistoryTime(item)).getTime();
      if (!Number.isFinite(ts) || ts < start || ts > now) return;
      const index = Math.min(bucketCount - 1, Math.max(0, Math.floor((ts - start) / bucketMs)));
      if (item.alertState === 'Firing') buckets[index].firing += 1;
      if (item.alertState === 'Recovered') buckets[index].recovered += 1;
    });

    const maxValue = Math.max(1, ...buckets.flatMap((b) => [b.firing, b.recovered]));
    const toPoints = (key) =>
      buckets
        .map((bucket, index) => {
          const x = (index / (bucketCount - 1)) * 320;
          const y = 82 - (bucket[key] / maxValue) * 68;
          return `${x},${y}`;
        })
        .join(' ');

    return {
      firing: toPoints('firing'),
      recovered: toPoints('recovered'),
      fireTotal: buckets.reduce((sum, item) => sum + item.firing, 0),
      recoveredTotal: buckets.reduce((sum, item) => sum + item.recovered, 0),
      hasData: buckets.some((item) => item.firing > 0 || item.recovered > 0),
    };
  }, [items]);

  return (
    <div className="trendChart">
      <div className="trendChart__legend">
        <span className="isFire">{_l('告警')} {chart.fireTotal}</span>
        <span className="isOk">{_l('恢复')} {chart.recoveredTotal}</span>
      </div>
      <svg viewBox="0 0 320 96" preserveAspectRatio="none">
        {[18, 38, 58, 78].map((y) => (
          <line key={y} x1="0" y1={y} x2="320" y2={y} />
        ))}
        {chart.hasData && (
          <>
            <polyline className="trendChart__fire" points={chart.firing} />
            <polyline className="trendChart__ok" points={chart.recovered} />
          </>
        )}
      </svg>
      {!chart.hasData && <div className="trendChart__empty">{_l('近 24 小时暂无告警事件')}</div>}
      <div className="trendChart__axis">
        <span>{_l('24 小时前')}</span>
        <span>{_l('现在')}</span>
      </div>
    </div>
  );
}

export default Overview;
