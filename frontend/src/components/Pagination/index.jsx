import React, { memo } from 'react';
import { Pagination } from 'antd';

import { _l } from '@/utils/utils';

import './index.less';

const CustomPagination = ({
  current,
  total,
  onChange,
  pageSize = 10,
  showSizeChanger = false,
  ...rest
}) => {
  const itemRender = (_, type, originalElement) => {
    if (type === 'prev') {
      return <a>{_l('上一页')}</a>;
    }
    if (type === 'next') {
      return <a>{_l('下一页')}</a>;
    }
    return originalElement;
  };

  return (
    <div className="paginationContainer">
      {total > pageSize && (
        <Pagination
          current={current}
          pageSize={pageSize}
          total={total}
          onChange={onChange}
          showSizeChanger={showSizeChanger}
          itemRender={itemRender}
          {...rest}
        />
      )}
    </div>
  );
};

export default memo(CustomPagination);
