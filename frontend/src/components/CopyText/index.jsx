import React, { memo } from 'react';
import { CopyToClipboard } from 'react-copy-to-clipboard';
import { message,Tooltip } from 'antd';

import { _l } from '@/utils/utils';

import './index.less';

const CopyText = ({ text }) => {
  const copyCallback = () => {
    message.success(_l('复制成功'));
  };

  return (
    <CopyToClipboard text={text} onCopy={copyCallback}>
      <Tooltip title={text}>
        <div className="copyText ellipsis">{text}</div>
      </Tooltip>
    </CopyToClipboard>
  );
};

export default memo(CopyText);
