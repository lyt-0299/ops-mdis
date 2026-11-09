import React from 'react';
import { useNavigate } from 'react-router';
import { Button,Result } from 'antd';

import { _l } from '@/utils/utils';

import './index.less';

const NotFound = () => {
  const navigate = useNavigate();
  return (
    <div className="notFountContainer">
      <Result
        status="404"
        title="404"
        subTitle={_l('抱歉，您访问的页面不存在')}
        extra={
          <Button type="primary" onClick={() => navigate('/')}>
            {_l('返回首页')}
          </Button>
        }
      />
    </div>
  )
};

export default NotFound;
