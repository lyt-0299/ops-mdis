import React, { useState } from 'react';
import { SearchOutlined } from '@ant-design/icons';
import { Button,Input, Select, Table } from 'antd';

import { _l } from '@/utils/utils';

import './index.less';

const WorksheetSharding = () => {
  const [searchValue, setSearchValue] = useState('');

  const handleChange = (value) => {
    console.log(`selected ${value}`);
  };

  return (
    <div className="worksheetShardingContainer">
      <div className="descriptionContainer">
        {_l(
          '支持对工作表开启分片存储。请勿在业务高峰期开启分片存储，以免影响性能和用户体验。'
        )}
      </div>
      <div className="searchBox">
        <div className="searchItemBox mr20">
          <span className="searchLabel">{_l('数据库')}</span>
          <Select
            defaultValue="mysql1"
            style={{ width: 200 }}
            size="large"
            onChange={handleChange}
            options={[
              { value: 'mysql1', label: 'mysql1' },
              { value: 'mysql2', label: 'mysql2' },
            ]}
          />
        </div>
        <div className="searchItemBox mr20">
          <span className="searchLabel">{_l('分片存储')}</span>
          <Select
            defaultValue="mysql1"
            style={{ width: 200 }}
            size="large"
            onChange={handleChange}
            options={[
              { value: 'mysql1', label: 'mysql1' },
              { value: 'mysql2', label: 'mysql2' },
            ]}
          />
        </div>
        <Input
          className="mr12"
          value={searchValue}
          style={{ width: 200 }}
          placeholder={_l('表名/表ID')}
          size="large"
          prefix={<SearchOutlined style={{ color: '#757575' }} />}
          onChange={(e) => setSearchValue(e.target.value)}
        />
        {/* <Button type="primary">查询</Button> */}
      </div>
    </div>
  );
};

export default WorksheetSharding;
