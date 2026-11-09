import React, { useEffect, useState } from 'react';
import { useLocation,useNavigate } from 'react-router';
import { Tabs } from 'antd';

import { getRouteKey } from '@/utils/utils';

const renderCommonLabel = (label) => {
  return <div style={{ padding: '0 10px' }}>{label}</div>;
};

const CusTabs = () => {
  const tabsMap = {
    host: [{ key: '/host', label: renderCommonLabel(_l('监控')) }],
    mysql: [{ key: '/mysql', label: renderCommonLabel(_l('监控')) }],
    mongodb: [
      { key: '/mongodb', label: renderCommonLabel(_l('监控')) },
      {
        key: '/mongodb/slowQuery',
        label: (
          <div style={{ padding: '0 10px 0 15px' }}>
            {_l('慢查询')}
          </div>
        ),
      },
    ],
    redis: [{ key: '/redis', label: renderCommonLabel(_l('监控')) }],
    kafka: [{ key: '/kafka', label: renderCommonLabel(_l('监控')) }],
    elasticsearch: [
      { key: '/elasticsearch', label: renderCommonLabel(_l('监控')) },
    ],
    // 自定义页面(/custom/*)没有 tab —— 它只有一个视图，tabsMap 取不到即隐藏 tab 栏。
  };
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [tabItems, setTabItems] = useState([]);
  const [activeKey, setActiveKey] = useState('');

  const handleOnChange = (key) => {
    navigate(key);
  };

  useEffect(() => {
    let key = getRouteKey();
    setTabItems(tabsMap[key]);
    setActiveKey(pathname);
  }, [pathname]);

  // Hide the tab bar for pages that only have the default monitoring view.
  if (!tabItems || tabItems.length <= 1) return null;

  return (
    <div style={{ height: 60 }}>
      <Tabs
        size="large"
        activeKey={activeKey}
        tabBarGutter={20}
        items={tabItems}
        onChange={handleOnChange}
      />
    </div>
  );
};

export default CusTabs;
