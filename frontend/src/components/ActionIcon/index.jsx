import React from 'react';
import {
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  HistoryOutlined,
  LinkOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import { Button, Tooltip } from 'antd';

const ICON_MAP = {
  test: LinkOutlined,
  edit: EditOutlined,
  delete: DeleteOutlined,
  refresh: ReloadOutlined,
  pause: PauseCircleOutlined,
  resume: PlayCircleOutlined,
  history: HistoryOutlined,
  detail: UnorderedListOutlined,
  view: EyeOutlined,
};

const ActionIcon = ({ type, title, onClick, danger, loading, disabled }) => {
  const Icon = ICON_MAP[type] || EyeOutlined;
  return (
    <Tooltip title={title}>
      <Button
        className="opsActionIcon"
        type="text"
        size="small"
        danger={danger}
        icon={<Icon spin={type === 'refresh' && loading} />}
        loading={type !== 'refresh' ? loading : false}
        disabled={disabled}
        aria-label={title}
        onClick={onClick}
      />
    </Tooltip>
  );
};

export default ActionIcon;
