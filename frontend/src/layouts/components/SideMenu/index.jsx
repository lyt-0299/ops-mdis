import React, { memo,useEffect, useState } from 'react';
import { useSelector } from 'react-redux';
import { useLocation,useNavigate } from 'react-router';
import { AlertOutlined, ApiOutlined,AppstoreOutlined, BarChartOutlined, DashboardOutlined, DatabaseFilled, FileTextOutlined, NodeIndexOutlined } from '@ant-design/icons';
import { Menu } from 'antd';

import { getRouteKey } from '@/utils/utils';

const MENU_GROUP_ICON = {
  'alert/overview': <DashboardOutlined />,
  datasources: <ApiOutlined />,
  'monitor-panels': <BarChartOutlined />,
  resource: <DatabaseFilled />,
  alert: <AlertOutlined />,
  log: <FileTextOutlined />,
  trace: <NodeIndexOutlined />,
  custom: <AppstoreOutlined />,
};

const DEFAULT_OPEN_KEYS = ['resource', 'alert', 'log', 'trace', 'custom'];

const SideMenu = ({ collapsed = false }) => {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const menuItems = useSelector((state) => state.iframe.menuItems);
  const [selectedKeys, setSelectedKeys] = useState([]);
  const [openKeys, setOpenKeys] = useState(collapsed ? [] : DEFAULT_OPEN_KEYS);

  const handleClick = ({ key }) => {
    // Keys for native alert sub-pages already include the full sub-path
    // (e.g. "alert/datasources"). Keys for other sections are bare slugs
    // (e.g. "host", "mongodb"). Prepend a single "/" in both cases.
    navigate(`/${key}`);
    setSelectedKeys([key]);
  };

  useEffect(() => {
    // For nested /alert/* routes the menu item key is "alert/<sub>" not just "alert".
    // Build the key from the full path segments (strip the base href first).
    const base = (document.querySelector('base')?.getAttribute('href') || '/web/').replace(/\/$/, '');
    const routePath = pathname.startsWith(base) ? pathname.slice(base.length) : pathname;
    // segments: ['alert', 'datasources'] → key = 'alert/datasources'
    // 自定义页面同理：['custom','langfuse'] → 'custom/langfuse'（一个 /custom 路由下
    // 挂着任意多个页面，只取第一段会让整组永远选不中）。
    const segments = routePath.split('/').filter(Boolean);
    let key;
    if ((segments[0] === 'alert' || segments[0] === 'custom') && segments.length > 1) {
      key = segments.slice(0, 2).join('/');
    } else {
      key = getRouteKey();
    }
    setSelectedKeys([key]);
  }, [pathname]);

  useEffect(() => {
    setOpenKeys(collapsed ? [] : DEFAULT_OPEN_KEYS);
  }, [collapsed]);

  const menuOpenProps = collapsed
    ? {}
    : {
        openKeys,
        onOpenChange: setOpenKeys,
      };

  return (
    <Menu
      key={collapsed ? 'collapsed' : 'expanded'}
      mode="inline"
      inlineCollapsed={collapsed}
      style={{ height: '100%', width: '100%' }}
      selectedKeys={selectedKeys}
      onClick={handleClick}
      {...menuOpenProps}
      items={menuItems.map((item) => ({
        key: item.key,
        label: item.label,
        icon: MENU_GROUP_ICON[item.key],
        children: item.children,
      }))}
    />
  );
};

export default memo(SideMenu);
