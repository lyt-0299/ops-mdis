import React, { Fragment,useEffect, useState } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import { useLocation, useNavigate } from 'react-router';
import { MenuFoldOutlined, MenuUnfoldOutlined } from '@ant-design/icons';
import { Button, Spin } from 'antd';

import { BASE_PATH } from '@/config';
import { updateIframeUrls, updateMenuItems } from '@/store/slices/iframe';
import { fetchSources, selectEnabledTypes } from '@/store/slices/sources';
import { getIframeUrls } from '@/utils/api';
import { _l } from '@/utils/utils';

import Header from '../components/Header';
import SideMenu from '../components/SideMenu';

import './index.less';

const IFRAME_ROUTE_KEYS = new Set([
  'host',
  'mysql',
  'redis',
  'kafka',
  'elasticsearch',
  'k8s',
  'custom',
  'containerConsole',
  'serviceLogs',
  'apm',
]);

const SIDEBAR_COLLAPSED_KEY = 'mdisSidebarCollapsed';

const isIframeRoute = (pathname) => {
  const routePath = pathname.startsWith(BASE_PATH) ? pathname.slice(BASE_PATH.length) : pathname;
  const segments = routePath.split('/').filter(Boolean);
  if (!segments.length) return false;
  if (segments[0] === 'mongodb' && segments[1] === 'slowQuery') return false;
  return IFRAME_ROUTE_KEYS.has(segments[0]);
};

/**
 * 顶级「概览」菜单项 — 原生 /alert/overview 路由，无 children。
 * SideMenu 的 handleClick({ key }) 调用 navigate(`/${key}`)，
 * 所以 key 直接对应路由路径（去掉开头的 /）。
 */
const getOverviewMenuItem = () => ({
  key: 'alert/overview',
  label: _l('概览'),
});

/**
 * 顶级「数据源」菜单项 — 原生 /datasources 聚合管理页，无 children。
 */
const getDatasourcesMenuItem = () => ({
  key: 'datasources',
  label: _l('数据源'),
});

/**
 * 告警分组 — 静态菜单组（原生 /alert/* 路由，不依赖 /api/iframe-urls 响应）。
 * 收敛为：告警规则 / 告警历史 / 通知渠道。
 * （旧的逐类型告警项 基础资源/端口/SSL/Kafka堆积/Kafka重平衡/MongoDB/MySQL/数据源管理
 *   已移除，统一进 /alert/rules 单页 tab。）
 *
 * 新增页面时：
 *   1. 在下面 children 添加条目
 *   2. 在 src/routes/index.js 添加对应路由
 */
const getAlertMenuGroup = () => ({
  key: 'alert',
  label: _l('告警管理'),
  children: [
    { key: 'alert/rules',    label: _l('告警规则') },
    { key: 'alert/history',  label: _l('告警历史') },
    { key: 'alert/webhooks', label: _l('通知渠道') },
  ],
});

/**
 * 资源监控组里「按启用数据源动态显示」的菜单 key → 数据源 type 映射。
 * 仅当 selectEnabledTypes 含对应 type 时，该菜单项才渲染。
 * host / k8s 不在此表内，始终显示（受 iframe-urls 是否返回控制）；
 * 自定义页面自成一组，也不受数据源启用态影响。
 */
const DATASOURCE_GATED_KEYS = {
  mysql: 'mysql',
  mongodb: 'mongodb',
  redis: 'redis',
  kafka: 'kafka',
  elasticsearch: 'elasticsearch',
};

const SidebarLayout = ({ children }) => {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const dispatch = useDispatch();
  const [loading, setLoading] = useState(true);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    return window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === '1';
  });
  // 原始 /api/iframe-urls 响应（分组 → 子项 map）。缓存下来，
  // 以便启用数据源变化时无需重新请求即可重建菜单。
  const [iframeData, setIframeData] = useState(null);
  // 已启用数据源 type 列表（如 ['kafka','mongodb']）。
  // 数据源页改动启用态后会 dispatch(fetchSources())，此处订阅自动重渲染。
  const enabledTypes = useSelector(selectEnabledTypes);
  const sourcesLoaded = useSelector((state) => state.sources.loaded);

  // Dynamic groups from /api/iframe-urls — resource / log / trace.
  // The "alert" key is intentionally excluded here; it is replaced by
  // the static ALERT_MENU_GROUP above so clicking items navigates to
  // native /alert/* routes instead of Grafana iframes.
  const MENU_GROUP_MAP = {
    resource: _l('资源监控'),
    // alert is excluded — handled as static group below
    log: _l('日志管理'),
    trace: _l('链路追踪'),
  };

  const MENU_MAP = {
    host: {
      key: 'host',
      label: 'Host',
    },
    mysql: {
      key: 'mysql',
      label: 'MySQL',
    },
    mongodb: {
      key: 'mongodb',
      label: 'MongoDB',
    },
    redis: {
      key: 'redis',
      label: 'Redis',
    },
    kafka: {
      key: 'kafka',
      label: 'Kafka',
    },
    elasticsearch: {
      key: 'elasticsearch',
      label: 'Elasticsearch',
    },
    k8s: {
      key: 'k8s',
      label: 'Kubernetes',
    },
    // alertRules / notifications / silences intentionally removed —
    // old iframe-based alert items; replaced by static ALERT_MENU_GROUP.
    containerConsole: {
      key: 'containerConsole',
      label: _l('容器日志'),
    },
    serviceLogs: {
      key: 'serviceLogs',
      label: _l('服务日志'),
    },
    tempo: {
      key: 'apm',
      label: _l('调用链分析'),
    },
  };

  // ① 拉取 iframe-urls（一次）+ 确保启用数据源已加载（供动态过滤用）。
  useEffect(() => {
    const fetchData = async () => {
      try {
        setLoading(true);
        let res = await getIframeUrls();
        setLoading(false);
        let data = res.data || {};
        // custom 是数组(条目名由用户起)，不能跟 resource/log/trace 那些 {key:url} 一起
        // 无脑 Object.assign —— 那会把数组下标当成菜单键塞进去。它单独展平成 custom/<id>。
        const { custom: customPages = [], ...urlGroups } = data;
        let iframeUrls = Object.assign({}, ...Object.values(urlGroups));
        (Array.isArray(customPages) ? customPages : []).forEach((page) => {
          if (page && page.key) iframeUrls[page.key] = page.url;
        });
        if (iframeUrls.tempo && !iframeUrls.apm) {
          iframeUrls.apm = iframeUrls.tempo;
        }
        setIframeData(data);
        dispatch(updateIframeUrls({ ...iframeUrls }));
      } catch (error) {
        // 401：拦截器已立即 replace 到登录页，这里保持 loading(继续显示 Spin)、
        // 不渲染主框架，避免"空壳一闪再跳登录"的闪烁。
        const status = error?.status || error?.response?.status;
        if (status === 401) return;
        setLoading(false);
        console.log(error);
      }
    };
    fetchData();
    // 数据源页改动启用态后已会 dispatch(fetchSources())；这里仅在尚未加载时补一次。
    if (!sourcesLoaded) {
      dispatch(fetchSources());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ② 根据 iframe-urls 数据 + 已启用数据源构建菜单。
  //    enabledTypes 变化（数据源启用/停用）时自动重建并重渲染。
  useEffect(() => {
    if (!iframeData) return;
    const data = iframeData;
    // 后端完全没返回任何可观测性分组（resource/log/trace）→ 视为未安装。
    if (!Object.keys(MENU_GROUP_MAP).some((k) => data[k])) {
      navigate('/notInstall');
      return;
    }
    let resourceGroup = null;
    let logTraceGroups = [];

    for (const [key, value] of Object.entries(data)) {
      // 后端可能返回 "alert" 分组（旧 iframe 告警）—— 跳过，
      // 改用静态 ALERT_MENU_GROUP。后端不再返回 alert 也无妨。
      if (key === 'alert') continue;
      if (!MENU_GROUP_MAP[key]) continue;

      const children = [];
      for (const [subKey, subUrl] of Object.entries(value)) {
        if (!MENU_MAP[subKey]) continue;
        // iframe URL 为空（如未配置 Flink 时后端返回空串）→ 不显示该菜单项。
        if (!subUrl) continue;
        // 资源监控组里受数据源启用态控制的项：未启用则不显示。
        const gatedType = DATASOURCE_GATED_KEYS[subKey];
        if (key === 'resource' && gatedType && !enabledTypes.includes(gatedType)) {
          continue;
        }
        children.push(MENU_MAP[subKey]);
      }
      const group = { key, label: MENU_GROUP_MAP[key], children };
      if (key === 'resource') {
        resourceGroup = group;
      } else {
        logTraceGroups.push(group);
      }
    }

    // 自定义页面：后端给什么就显示什么（名字是用户自己起的，前端没有静态映射表）。
    // 一条都没配时整组不出现。
    const customChildren = (Array.isArray(data.custom) ? data.custom : [])
      .filter((page) => page && page.key && page.url)
      .map((page) => ({ key: page.key, label: page.name }));

    // 目标顺序：概览 → 数据源 → 资源监控 → 告警 → 日志 → 链路 → 自定义
    const menuGroup = [getOverviewMenuItem(), getDatasourcesMenuItem()];
    if (resourceGroup) menuGroup.push(resourceGroup);
    menuGroup.push({ key: 'monitor-panels', label: _l('自定义监控') });
    menuGroup.push(getAlertMenuGroup());
    menuGroup.push(...logTraceGroups);
    if (customChildren.length) {
      menuGroup.push({ key: 'custom', label: _l('自定义'), children: customChildren });
    }

    dispatch(updateMenuItems(menuGroup));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [iframeData, enabledTypes]);

  const toggleSidebar = () => {
    setSidebarCollapsed((collapsed) => {
      const nextCollapsed = !collapsed;
      window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, nextCollapsed ? '1' : '0');
      return nextCollapsed;
    });
  };

  return (
    <Fragment>
      {loading ? (
        <Spin fullscreen></Spin>
      ) : (
        <div className="sidebarLayout">
          <Header />
          <div className="sidebarContentContainer">
            <div className={`menuBox${sidebarCollapsed ? ' menuBox--collapsed' : ''}`}>
              <Button
                className="menuCollapseBtn"
                type="text"
                size="small"
                icon={sidebarCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
                title={sidebarCollapsed ? _l('展开导航') : _l('折叠导航')}
                aria-label={sidebarCollapsed ? _l('展开导航') : _l('折叠导航')}
                onClick={toggleSidebar}
              />
              <SideMenu collapsed={sidebarCollapsed}></SideMenu>
            </div>
            <div className={`contentBox${isIframeRoute(pathname) ? ' contentBox--iframe' : ''}`}>
              {children}
            </div>
          </div>
        </div>
      )}
    </Fragment>
  );
};

export default SidebarLayout;
