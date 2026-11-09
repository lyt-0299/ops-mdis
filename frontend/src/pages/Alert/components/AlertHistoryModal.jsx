/**
 * AlertHistoryModal — reusable alert-history dialog.
 *
 * Props:
 *   open        {boolean}   controlled visibility
 *   onClose     {Function}  close callback
 *   title       {string}    rule/monitor name shown in the header
 *   fetchHistory {Function} (page, pageSize) => Promise<{ data: { items, total } }>
 */
import React, { useCallback,useEffect, useState } from 'react';
import { ClockCircleOutlined } from '@ant-design/icons';
import { Modal, Table, Tag, Tooltip } from 'antd';

import { _l } from '@/utils/utils';

const PAGE_SIZE = 20;

const STATE_META = {
  Firing:     { color: 'error',   text: _l('告警') },
  Recovered:  { color: 'success', text: _l('恢复') },
  Muted:      { color: 'warning', text: _l('静默') },
  Unmuted:    { color: 'processing', text: _l('恢复通知') },
  Suppressed: { color: 'default', text: _l('静默') },
};

function formatTime(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  } catch {
    return iso;
  }
}

function relativeTime(iso) {
  if (!iso) return '';
  const diff = (Date.now() - new Date(iso).getTime()) / 1000;
  if (diff < 60)    return `${Math.floor(diff)}${_l('秒前')}`;
  if (diff < 3600)  return `${Math.floor(diff / 60)}${_l('分钟前')}`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}${_l('小时前')}`;
  return `${Math.floor(diff / 86400)}${_l('天前')}`;
}

const AlertHistoryModal = ({ open, onClose, title, fetchHistory }) => {
  const [loading, setLoading] = useState(false);
  const [items, setItems]     = useState([]);
  const [total, setTotal]     = useState(0);
  const [page, setPage]       = useState(1);

  const loadPage = useCallback(
    (p) => {
      if (!fetchHistory) return;
      setLoading(true);
      fetchHistory(p, PAGE_SIZE)
        .then(({ data }) => {
          setItems(Array.isArray(data?.items) ? data.items : []);
          setTotal(data?.total ?? 0);
        })
        .catch(() => {})
        .finally(() => setLoading(false));
    },
    [fetchHistory],
  );

  useEffect(() => {
    if (open) {
      setPage(1);
      loadPage(1);
    }
  }, [open, loadPage]);

  // 10-second silent refresh while open
  useEffect(() => {
    if (!open) return;
    const timer = setInterval(() => loadPage(page), 10000);
    return () => clearInterval(timer);
  }, [open, page, loadPage]);

  const columns = [
    {
      title: _l('时间'),
      dataIndex: 'triggeredAt',
      width: 200,
      render: (v) => (
        <Tooltip title={relativeTime(v)}>
          <span style={{ fontVariantNumeric: 'tabular-nums', fontSize: 12 }}>
            {formatTime(v)}
          </span>
        </Tooltip>
      ),
    },
    {
      title: _l('状态'),
      dataIndex: 'alertState',
      width: 100,
      render: (v) => {
        const m = STATE_META[v] || { color: 'default', text: v };
        return <Tag color={m.color}>{m.text}</Tag>;
      },
    },
    {
      title: _l('详情'),
      dataIndex: 'content',
      ellipsis: true,
      render: (v) => (
        <Tooltip title={<pre style={{ maxWidth: 480, whiteSpace: 'pre-wrap', fontSize: 11, margin: 0 }}>{v}</pre>}>
          <span style={{ fontFamily: 'monospace', fontSize: 12, color: '#555' }}>{v}</span>
        </Tooltip>
      ),
    },
  ];

  return (
    <Modal
      title={
        <span>
          <ClockCircleOutlined style={{ color: '#5b6af0', marginRight: 8 }} />
          {_l('告警历史')}
          {title && (
            <span style={{ fontSize: 12, color: '#888', marginLeft: 8, fontWeight: 400 }}>
              — {title}
            </span>
          )}
        </span>
      }
      open={open}
      onCancel={onClose}
      footer={null}
      width={860}
      destroyOnClose
    >
      <Table
        loading={loading}
        columns={columns}
        dataSource={items}
        rowKey="id"
        size="small"
        pagination={{
          current: page,
          pageSize: PAGE_SIZE,
          total,
          showSizeChanger: false,
          showTotal: (t) => `${_l('共')} ${t} ${_l('条记录')}`,
          onChange: (p) => {
            setPage(p);
            loadPage(p);
          },
        }}
        locale={{ emptyText: _l('暂无告警历史记录') }}
      />
    </Modal>
  );
};

export default AlertHistoryModal;
