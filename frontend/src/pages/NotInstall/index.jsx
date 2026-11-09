import React from 'react';
import { ApiOutlined } from '@ant-design/icons';

import { _l } from '@/utils/utils';

import './index.less';

const NotInstall = () => {
  return (
    <div className="notInstallContainer">
      <div className="notInstallContent">
        <div className="notInstallIconBox">
          <ApiOutlined className="apiIcon" />
        </div>
        <div className="tips">
          {_l('未部署管理器扩展组件，请参考部署文档')}
        </div>
      </div>
    </div>
  )
}

export default NotInstall;
