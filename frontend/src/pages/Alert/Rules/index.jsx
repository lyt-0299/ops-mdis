import React, { useCallback,useMemo } from 'react';
import { useSelector } from 'react-redux';
import { useLocation,useNavigate } from 'react-router';
import { Tabs } from 'antd';

import { selectEnabledTypes } from '@/store/slices/sources';
import { _l } from '@/utils/utils';

import AlertRules      from '../AlertRules';
import KafkaLag        from '../KafkaLag';
import KafkaRebalance  from '../KafkaRebalance';
import MongoMonitor    from '../MongoMonitor';
import MySQLMonitor    from '../MySQLMonitor';
import PortMonitor     from '../PortMonitor';
import SslMonitor      from '../SslMonitor';

import './index.less';

// ── Tab definitions ───────────────────────────────────────────────────────────
// `requires` 为空数组 → 常驻（基础资源/端口/SSL）。
// 否则只有当对应数据源 type 已启用时才显示。
const TAB_DEFS = [
  { key: 'infra',           label: _l('基础资源'),   Comp: AlertRules,     requires: [] },
  { key: 'port',            label: _l('端口'),       Comp: PortMonitor,    requires: [] },
  { key: 'ssl',             label: 'SSL',            Comp: SslMonitor,     requires: [] },
  { key: 'mysql',           label: 'MySQL',          Comp: MySQLMonitor,   requires: ['mysql'] },
  { key: 'mongo',           label: 'MongoDB',        Comp: MongoMonitor,   requires: ['mongodb'] },
  { key: 'kafka-lag',       label: _l('Kafka 堆积'), Comp: KafkaLag,       requires: ['kafka'] },
  { key: 'kafka-rebalance', label: _l('Kafka 重平衡'), Comp: KafkaRebalance, requires: ['kafka'] },
];

const DEFAULT_TAB = 'infra';

const AlertRulesTabs = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const enabledTypes = useSelector(selectEnabledTypes);

  // ── Visible tabs（按启用数据源过滤动态 tab）────────────────────────────────
  const visibleTabs = useMemo(
    () =>
      TAB_DEFS.filter(
        (t) =>
          t.requires.length === 0 ||
          t.requires.every((type) => enabledTypes.includes(type)),
      ),
    [enabledTypes],
  );

  // ── activeKey 由 URL query ?tab= 派生；不可见则回退 infra ───────────────────
  const activeKey = useMemo(() => {
    const params = new URLSearchParams(location.search);
    const tab = params.get('tab');
    return visibleTabs.some((t) => t.key === tab) ? tab : DEFAULT_TAB;
  }, [location.search, visibleTabs]);

  const onTabChange = useCallback(
    (key) => {
      navigate(`/alert/rules?tab=${key}`);
    },
    [navigate],
  );

  const items = useMemo(
    () =>
      visibleTabs.map(({ key, label, Comp }) => ({
        key,
        label,
        children: <Comp />,
      })),
    [visibleTabs],
  );

  return (
    <div className="alertRulesTabsContainer">
      <div className="opsPageHeader">
        <div className="opsPageTitle">{_l('告警规则')}</div>
      </div>
      <Tabs activeKey={activeKey} onChange={onTabChange} items={items} />
    </div>
  );
};

export default AlertRulesTabs;
