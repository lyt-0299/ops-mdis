import React, { useCallback, useEffect, useMemo,useState } from 'react';
import {
  ClearOutlined,
  DeleteOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import {
  Button,
  DatePicker,
  Empty,
  Input,
  message,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
} from 'antd';

import {
  batchDeleteAlertHistory,
  deleteAllAlertHistory,
  getAlertHistorySourceTypes,
  listGlobalAlertHistory,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

import './index.less';

const { RangePicker } = DatePicker;
const { Option } = Select;

// ── Constants ─────────────────────────────────────────────────────────────────

const ALERT_STATE_OPTIONS = [
  { value: 'Firing',    label: _l('告警') },
  { value: 'Recovered', label: _l('恢复') },
  { value: 'Muted',     label: _l('静默') },
  { value: 'Unmuted',   label: _l('恢复通知') },
];

const SORT_OPTIONS = [
  { value: 'desc', label: _l('最新优先') },
  { value: 'asc',  label: _l('最早优先') },
];

const SOURCE_TYPE_LABELS = {
  prometheus: _l('基础资源'),
  port_monitor: _l('端口连通性'),
  ssl_monitor: _l('SSL 证书'),
  mysql_monitor: _l('MySQL'),
  mongo_monitor: _l('MongoDB'),
  kafka: _l('Kafka 堆积'),
  kafka_rebalance_monitor: _l('Kafka 重平衡'),
};

const STATE_TAG = {
  Firing:    { color: 'error',      text: _l('告警') },
  Recovered: { color: 'success',    text: _l('恢复') },
  Muted:     { color: 'warning',    text: _l('静默') },
  Unmuted:   { color: 'processing', text: _l('恢复通知') },
};

const DEFAULT_PAGE_SIZE = 20;

// ── Helpers ───────────────────────────────────────────────────────────────────

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

function getSourceTypeLabel(value) {
  return SOURCE_TYPE_LABELS[value] || value || '-';
}

// ── Main component ────────────────────────────────────────────────────────────

const AlertHistory = () => {
  // ── Data state ──────────────────────────────────────────────────────────────
  const [loading, setLoading]             = useState(false);
  const [items, setItems]                 = useState([]);
  const [total, setTotal]                 = useState(0);
  const [page, setPage]                   = useState(1);
  const [pageSize, setPageSize]           = useState(DEFAULT_PAGE_SIZE);
  const [sourceTypeOptions, setSourceTypeOptions] = useState([]);

  // ── Filter state ─────────────────────────────────────────────────────────────
  const [filterSourceType,  setFilterSourceType]  = useState(undefined);
  const [filterSourceName,  setFilterSourceName]  = useState('');
  const [filterAlertState,  setFilterAlertState]  = useState(undefined);
  const [filterTimeRange,   setFilterTimeRange]   = useState(null); // [dayjs, dayjs] | null
  const [sortOrder,         setSortOrder]         = useState('desc');

  // ── Selection ────────────────────────────────────────────────────────────────
  const [selectedRowKeys, setSelectedRowKeys] = useState([]);

  // ── Load source types once ────────────────────────────────────────────────────
  useEffect(() => {
    getAlertHistorySourceTypes()
      .then(({ data }) => {
        const list = Array.isArray(data) ? data : [];
        setSourceTypeOptions(
          list
            .map((item) => (typeof item === 'string' ? item : item?.value))
            .filter(Boolean)
            .map((value) => ({
              value,
              label: getSourceTypeLabel(value),
            })),
        );
      })
      .catch(() => {});
  }, []);

  // ── Fetch data ────────────────────────────────────────────────────────────────
  const fetchData = useCallback(
    (p = 1, ps = pageSize) => {
      setLoading(true);
      const params = {
        page: p,
        pageSize: ps,
        sort: sortOrder,
      };
      if (filterSourceType) params.sourceType = filterSourceType;
      if (filterSourceName) params.sourceName = filterSourceName;
      if (filterAlertState) params.alertState = filterAlertState;
      if (filterTimeRange && filterTimeRange[0]) {
        params.startTime = filterTimeRange[0].toISOString();
      }
      if (filterTimeRange && filterTimeRange[1]) {
        params.endTime = filterTimeRange[1].toISOString();
      }

      listGlobalAlertHistory(params)
        .then(({ data }) => {
          setItems(Array.isArray(data?.items) ? data.items : []);
          setTotal(data?.total ?? 0);
        })
        .catch((e) => message.error(apiErr(e, _l('获取告警历史失败'))))
        .finally(() => setLoading(false));
    },
     
    [filterSourceType, filterSourceName, filterAlertState, filterTimeRange, sortOrder, pageSize],
  );

  // Initial load + filter change
  useEffect(() => {
    setPage(1);
    setSelectedRowKeys([]);
    fetchData(1, pageSize);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterSourceType, filterSourceName, filterAlertState, filterTimeRange, sortOrder]);

  // ── Handlers ──────────────────────────────────────────────────────────────────

  const handleReset = () => {
    setFilterSourceType(undefined);
    setFilterSourceName('');
    setFilterAlertState(undefined);
    setFilterTimeRange(null);
    setSortOrder('desc');
    setSelectedRowKeys([]);
  };

  const handleRefresh = () => {
    setSelectedRowKeys([]);
    fetchData(page, pageSize);
  };

  const handlePageChange = (p, ps) => {
    setPage(p);
    setPageSize(ps);
    setSelectedRowKeys([]);
    fetchData(p, ps);
  };

  const handleBatchDelete = () => {
    if (selectedRowKeys.length === 0) return;
    batchDeleteAlertHistory(selectedRowKeys)
      .then(() => {
        message.success(_l('删除成功'));
        setSelectedRowKeys([]);
        fetchData(page, pageSize);
      })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  const handleDeleteAll = () => {
    const params = {};
    if (filterSourceType) params.sourceType = filterSourceType;
    if (filterSourceName) params.sourceName = filterSourceName;
    if (filterAlertState) params.alertState = filterAlertState;
    if (filterTimeRange && filterTimeRange[0]) {
      params.startTime = filterTimeRange[0].toISOString();
    }
    if (filterTimeRange && filterTimeRange[1]) {
      params.endTime = filterTimeRange[1].toISOString();
    }

    deleteAllAlertHistory(params)
      .then(() => {
        message.success(_l('已清空告警历史'));
        setSelectedRowKeys([]);
        setPage(1);
        fetchData(1, pageSize);
      })
      .catch((e) => message.error(apiErr(e, _l('清空失败，请重试'))));
  };

  // ── Table columns ─────────────────────────────────────────────────────────────

  const columns = useMemo(
    () => [
      {
        title: _l('时间'),
        dataIndex: 'triggeredAt',
        width: 180,
        render: (v) => (
          <span className="ahMonoText">{formatTime(v)}</span>
        ),
      },
      {
        title: _l('来源类型'),
        dataIndex: 'sourceType',
        width: 160,
        render: (v) => {
          return (
            <Tag>
              {getSourceTypeLabel(v)}
            </Tag>
          );
        },
      },
      {
        title: _l('来源名称'),
        dataIndex: 'sourceName',
        ellipsis: true,
        render: (v) => (
          <Tooltip title={v}>
            <span className="ahMonoText">{v || '-'}</span>
          </Tooltip>
        ),
      },
      {
        title: _l('状态'),
        dataIndex: 'alertState',
        width: 100,
        render: (v) => {
          const cfg = STATE_TAG[v] || { color: 'default', text: v };
          return <Tag color={cfg.color}>{cfg.text}</Tag>;
        },
      },
      {
        title: _l('详情'),
        dataIndex: 'content',
        ellipsis: true,
        render: (v) =>
          v ? (
            <Tooltip
              title={
                <pre className="ahTooltipPre">{v}</pre>
              }
            >
              <span className="ahContentText">{v}</span>
            </Tooltip>
          ) : (
            <span className="ahEmpty">-</span>
          ),
      },
      {
        title: _l('通知渠道'),
        dataIndex: 'webhookName',
        width: 130,
        ellipsis: true,
        render: (v) =>
          v ? (
            <Tag>{v}</Tag>
          ) : (
            <span className="ahEmpty">-</span>
          ),
      },
    ],
    [sourceTypeOptions],
  );

  // ── Render ────────────────────────────────────────────────────────────────────

  const hasSelection = selectedRowKeys.length > 0;

  return (
    <div className="alertHistoryContainer">
      {/* ── Toolbar ── */}
      <div className="opsPageHeader">
        <div className="opsPageTitle">{_l('告警历史')}</div>
      </div>

      <div className="ahToolbarControls">
          <div className="ahFilters">
            {/* Source type */}
            <Select
              allowClear
              placeholder={_l('来源类型')}
              style={{ width: 160 }}
              value={filterSourceType}
              onChange={(v) => setFilterSourceType(v)}
            >
              {sourceTypeOptions.map((o) => (
                <Option key={o.value} value={o.value}>{o.label}</Option>
              ))}
            </Select>

            {/* Alert state */}
            <Select
              allowClear
              placeholder={_l('告警状态')}
              style={{ width: 120 }}
              value={filterAlertState}
              onChange={(v) => setFilterAlertState(v)}
            >
              {ALERT_STATE_OPTIONS.map((o) => (
                <Option key={o.value} value={o.value}>{o.label}</Option>
              ))}
            </Select>

            {/* Time range */}
            <RangePicker
              showTime
              style={{ width: 360 }}
              value={filterTimeRange}
              onChange={(vals) => setFilterTimeRange(vals)}
              placeholder={[_l('开始时间'), _l('结束时间')]}
              format="YYYY-MM-DD HH:mm"
            />

            {/* Source name search */}
            <Input.Search
              placeholder={_l('搜索来源名称')}
              value={filterSourceName}
              onChange={(e) => setFilterSourceName(e.target.value)}
              onSearch={(v) => setFilterSourceName(v)}
              style={{ width: 200 }}
              allowClear
            />

            {/* Sort */}
            <Select
              value={sortOrder}
              style={{ width: 150 }}
              onChange={(v) => setSortOrder(v)}
            >
              {SORT_OPTIONS.map((o) => (
                <Option key={o.value} value={o.value}>{o.label}</Option>
              ))}
            </Select>

            {/* Reset */}
            <Button onClick={handleReset}>{_l('重置')}</Button>
          </div>

          {/* Action buttons */}
          <Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={handleRefresh}
          >
            {_l('刷新')}
          </Button>

          <Popconfirm
            title={_l('确认批量删除')}
            description={`${_l('将删除所选')} ${selectedRowKeys.length} ${_l('条记录，确认继续？')}`}
            okText={_l('删除')}
            okType="danger"
            cancelText={_l('取消')}
            onConfirm={handleBatchDelete}
            disabled={!hasSelection}
          >
            <Button
              danger
              icon={<DeleteOutlined />}
              disabled={!hasSelection}
            >
              {_l('批量删除')}
              {hasSelection ? ` (${selectedRowKeys.length})` : ''}
            </Button>
          </Popconfirm>

          <Popconfirm
            title={_l('确认清空全部')}
            description={_l('将清空当前筛选条件下的所有历史记录，此操作不可恢复，确认继续？')}
            okText={_l('清空')}
            okType="danger"
            cancelText={_l('取消')}
            onConfirm={handleDeleteAll}
          >
          <Button danger icon={<ClearOutlined />}>
            {_l('清空全部')}
          </Button>
        </Popconfirm>
          </Space>
      </div>

      {/* ── Table ── */}
      <div className="ahTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={items}
          rowKey="id"
          size="small"
          tableLayout="auto"
          scroll={{ x: 'max-content', y: 'calc(100vh - 230px)' }}
          rowSelection={{
            selectedRowKeys,
            onChange: (keys) => setSelectedRowKeys(keys),
            preserveSelectedRowKeys: false,
          }}
          pagination={{
            current: page,
            pageSize,
            total,
            showSizeChanger: true,
            pageSizeOptions: ['10', '20', '50', '100'],
            showTotal: (t) => `${_l('共')} ${t} ${_l('条记录')}`,
            onChange: handlePageChange,
            onShowSizeChange: handlePageChange,
          }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('暂无告警历史记录')}
              />
            ),
          }}
        />
      </div>
    </div>
  );
};

export default AlertHistory;
