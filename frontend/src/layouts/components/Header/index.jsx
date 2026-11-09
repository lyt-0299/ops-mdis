import React from 'react';
import { useNavigate } from 'react-router';
import { DownOutlined, GlobalOutlined, LogoutOutlined } from '@ant-design/icons';
import { Dropdown } from 'antd';
import _ from 'lodash';

import logo from '@/assets/logo.png';
import { logout } from '@/utils/api';
import langConfig from '@/utils/langConfig';
import { _l } from '@/utils/utils';

import './index.less';

function Header(props) {
  const navigate = useNavigate();
  const defaultLang = navigator.language === 'zh-CN' ? 'zh-Hans' : 'en';
  const lang = window.getCookie('i18n_langtag') || defaultLang;
  const currentLang = _.find(langConfig, { key: lang }) || langConfig[0];
  const langShortName = {
    'zh-Hans': 'CN',
    'zh-Hant': '繁',
    en: 'EN',
    ja: 'JA',
  };

  const handleMenuClick = (e) => {
    window.setCookie('i18n_langtag', e.key);
    window.location.reload();
  };

  const handleLogout = async () => {
    try {
      await logout();
    } finally {
      navigate('/auth', { replace: true });
    }
  };

  return (
    <div className="privateDeploymentHeader">
      <div className="logoWrap">
        <div><img src={logo} alt="logo" /></div>
        <div className="text">{_l('运维平台')}</div>
      </div>
      <div className="headerActions">
        <Dropdown
          menu={{
            items: langConfig,
            onClick: handleMenuClick,
            selectedKeys: [lang],
          }}
          trigger={['click']}
          overlayClassName="languageDropdown"
        >
          <button type="button" className="languageSwitch">
            <span className="languageSwitchIcon"><GlobalOutlined /></span>
            <span className="languageSwitchText">{langShortName[currentLang.key] || currentLang.label}</span>
            <DownOutlined className="languageSwitchArrow" />
          </button>
        </Dropdown>
        <button type="button" className="logoutButton" onClick={handleLogout} aria-label={_l('退出')} title={_l('退出')}>
          <LogoutOutlined />
        </button>
      </div>
    </div>
  );
}

export default Header;
