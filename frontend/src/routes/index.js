import React, { lazy } from 'react';
import { Navigate } from 'react-router';

import TabsLayout from '@/layouts/TabsLayout';
import Auth from '@/pages/Auth';
import Iframe from '@/pages/Iframe';
import NotFound from '@/pages/NotFound';
import NotInstall from '@/pages/NotInstall';

// 慢查询
const SlowQuery = lazy(() => import('@/pages/SlowQuery'));
// 工作表分片
const WorksheetSharding = lazy(() => import('@/pages/WorksheetSharding'));
// 顶级数据源聚合管理页
const DataSources = lazy(() => import('@/pages/DataSources'));
const MonitorPanels = lazy(() => import('@/pages/MonitorPanels'));

// ── 告警子系统 — 原生 Antd 页面 ───────────────────────────────────────────
// §1 数据源管理 (已完成)
const AlertDataSources = lazy(() => import('@/pages/Alert/DataSources'));
// §3 概览 (已完成)
const AlertOverview = lazy(() => import('@/pages/Alert/Overview'));
// §4 通知渠道 (已完成)
const AlertWebhooks = lazy(() => import('@/pages/Alert/Webhooks'));
// §4c 告警规则 — 类型 tab 单页（基础资源/端口/SSL 常驻 + 按启用数据源动态）
const AlertRulesTabs = lazy(() => import('@/pages/Alert/Rules'));
// §4e 全局告警历史 (已完成)
const AlertHistory = lazy(() => import('@/pages/Alert/AlertHistory'));

export const routes = [
  {
    path: '/',
    element: <Navigate to="/alert/overview" replace />,
  },
  {
    path: '/host',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/datasources',
    element: <DataSources />,
  },
  {
    path: '/monitor-panels',
    element: <MonitorPanels />,
  },
  {
    path: '/mysql',
    element: <TabsLayout />,
    children: [
      { path: '', element: <Iframe /> },
    ],
  },
  {
    path: '/mongodb',
    element: <TabsLayout />,
    children: [
      { path: '', element: <Iframe /> },
      { path: 'slowQuery', element: <SlowQuery /> },
      // { path: 'worksheetSharding', element: <WorksheetSharding /> },
    ],
  },
  {
    path: '/redis',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/kafka',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/elasticsearch',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/k8s',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  // 自定义页面：一条配置一个 slug，路由通配。旧的 /flink 是 1.5.11 及以前的固定路由，
  // 迁移后 Flink 就是 slug=flink 的一条自定义页面，这里保留一条重定向别让旧书签失效。
  {
    path: '/custom/:slug',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/flink',
    element: <Navigate to="/custom/flink" replace />,
  },
  {
    path: '/containerConsole',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/serviceLogs',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  {
    path: '/apm',
    element: <TabsLayout />,
    children: [{ path: '', element: <Iframe /> }],
  },
  // ── 告警子系统 — 原生路由 (/alert/*) ─────────────────────────────────────
  // 旧 iframe 路由 /alertRules /notifications /silences 已移除，
  // 不再重定向旧路径（后端 iframe-urls 的 alert 分组已不再渲染为菜单项）。
  {
    path: '/alert/datasources',
    element: <AlertDataSources />,
  },
  {
    path: '/alert/overview',
    element: <AlertOverview />,
  },
  // 告警规则合并单页：所有类型监控收敛到 /alert/rules，用 ?tab= 切换
  {
    path: '/alert/rules',
    element: <AlertRulesTabs />,
  },
  // 旧的分散路由保持可访问（不 404），重定向到合并单页对应 tab
  {
    path: '/alert/infra-monitor',
    element: <Navigate to="/alert/rules?tab=infra" replace />,
  },
  {
    path: '/alert/port',
    element: <Navigate to="/alert/rules?tab=port" replace />,
  },
  {
    path: '/alert/ssl',
    element: <Navigate to="/alert/rules?tab=ssl" replace />,
  },
  {
    path: '/alert/kafka-lag',
    element: <Navigate to="/alert/rules?tab=kafka-lag" replace />,
  },
  {
    path: '/alert/kafka-rebalance',
    element: <Navigate to="/alert/rules?tab=kafka-rebalance" replace />,
  },
  {
    path: '/alert/mongo',
    element: <Navigate to="/alert/rules?tab=mongo" replace />,
  },
  {
    path: '/alert/mysql',
    element: <Navigate to="/alert/rules?tab=mysql" replace />,
  },
  {
    path: '/alert/history',
    element: <AlertHistory />,
  },
  {
    path: '/alert/webhooks',
    element: <AlertWebhooks />,
  },
  {
    path: '/auth',
    element: <Auth />,
    layout: 'none',
  },
  {
    path: '/notInstall',
    element: <NotInstall />,
    layout: 'common',
  },
  {
    path: '/404',
    element: <NotFound />,
    layout: 'common',
  },
  {
    path: '*',
    element: <Navigate to="/404" replace />,
  },
];

export const getSupplyRoute = (key) => {
  return { path: `/${key}`, exact: true, component: Iframe, layout: 'sidebar' };
};
