import React, { useState } from 'react';
import { useNavigate } from 'react-router';
import { DownOutlined, GlobalOutlined, LockOutlined } from '@ant-design/icons';
import { Button, Dropdown, Input, message } from 'antd';

import logo from '@/assets/logo.png';
import { getAuth } from '@/utils/api';
import langConfig from '@/utils/langConfig';
import { _l } from '@/utils/utils';

import './index.less';

const Auth = () => {
  const navigate = useNavigate();
  const [messageApi, contextHolder] = message.useMessage();
  const [authValue, setAuthValue] = useState('');
  const [loading, setLoading] = useState(false);
  const defaultLang = navigator.language === 'zh-CN' ? 'zh-Hans' : 'en';
  const lang = window.getCookie('i18n_langtag') || defaultLang;
  const currentLang = langConfig.find(item => item.key === lang) || langConfig[0];
  const langShortName = {
    'zh-Hans': 'CN',
    'zh-Hant': '繁',
    en: 'EN',
    ja: 'JA',
  };

  const notice = (content, type = 'warning') => {
    messageApi.open({
      type,
      content,
    });
  };

  const fetchData = async () => {
    setLoading(true);
    try {
      await getAuth(authValue);
      navigate('/alert/overview');
    } catch (error) {
      console.log(error);
      notice(_l('Token 认证失败'), 'error');
    } finally {
      setLoading(false);
    }
  };

  const saveAuth = () => {
    if (!authValue.trim()) {
      notice(_l('请输入 Token'));
      return;
    }
    fetchData();
  };

  const handleLangClick = e => {
    window.setCookie('i18n_langtag', e.key);
    window.location.reload();
  };

  return (
    <div className="authPage">
      {contextHolder}
      <Dropdown
        menu={{
          items: langConfig,
          onClick: handleLangClick,
          selectedKeys: [lang],
        }}
        trigger={['click']}
        overlayClassName="languageDropdown authLanguageDropdown"
      >
        <button type="button" className="authLanguageSwitch">
          <GlobalOutlined />
          <span>{langShortName[currentLang.key] || currentLang.label}</span>
          <DownOutlined className="authLanguageArrow" />
        </button>
      </Dropdown>
      <div className="authShell">
        <section className="authBrandPanel">
          <div className="authLogoRow">
            <img src={logo} alt="Mingdao" />
            <span>{_l('运维平台')}</span>
          </div>
          <div className="authMonitorScene" aria-hidden="true">
            <span className="monitorRadarSweep" />
            <span className="monitorRadarCore" />
            <span className="monitorRadarDot monitorRadarDotOne" />
            <span className="monitorRadarDot monitorRadarDotTwo" />
            <span className="monitorRadarDot monitorRadarDotThree" />
          </div>
          <h1>{_l('统一运维监控平台')}</h1>
          <p>{_l('集中监控资源、告警、日志与调用链路，快速定位问题，提升系统稳定性。')}</p>
          <div className="authCapabilityList">
            <span><i />{_l('资源监控')}</span>
            <span><i />{_l('告警管理')}</span>
            <span><i />{_l('日志分析')}</span>
            <span><i />{_l('调用链路')}</span>
          </div>
        </section>

        <section className="authCard">
          <div className="authCardHeader">
            <div className="authIcon">
              <LockOutlined />
            </div>
            <div>
              <h2>{_l('登录运维平台')}</h2>
              <p className="authTokenTip">
                <span>{_l('Token 可在 ops.yaml 中查看 ENV_OPS_TOKEN')}</span>
              </p>
            </div>
          </div>
          <form
            onSubmit={e => {
              e.preventDefault();
              saveAuth();
            }}
          >
            {/* 浏览器密码管理器要认出这是登录表单才会提示保存：需要 form + 提交事件 + autoComplete
                标注。本站只有 Token 一个凭据，故补一个隐藏的 username 字段，让保存下来的条目有名字。 */}
            <input
              type="text"
              name="username"
              autoComplete="username"
              value="ops"
              readOnly
              tabIndex={-1}
              aria-hidden="true"
              className="authHiddenUser"
            />
            <Input.Password
              className="authTokenInput"
              size="large"
              value={authValue}
              name="token"
              autoComplete="current-password"
              prefix={<LockOutlined />}
              placeholder={_l('请输入 Token')}
              autoFocus
              onChange={e => setAuthValue(e.target.value)}
            />
            <Button type="primary" size="large" block loading={loading} htmlType="submit">
              {_l('登录')}
            </Button>
          </form>
        </section>
      </div>
    </div>
  );
};

export default Auth;
