import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  DatabaseOutlined,
  ExclamationCircleOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import {
  Button,
  Empty,
  Input,
  message,
  Modal,
  Select,
  Spin,
  Table,
  Tooltip,
  Typography,
} from 'antd';
import _ from 'lodash';
import moment from 'moment';

import Pagination from '@/components/Pagination';
import {
  createIndex,
  executeTask,
  getSlowQueryDetail,
  getSlowQueryList,
} from '@/utils/api';
import { _l, formatNumber, formatTime } from '@/utils/utils';

import './index.less';

const { Text } = Typography;

const SlowQuery = () => {
  const timerRef = useRef(null);
  const [currentDatabase, setCurrentDatabase] = useState('mdwsrows');
  const [databaseStatus, setDatabaseStatus] = useState({});
  // 列表相关
  const [searchValue, setSearchValue] = useState('');
  const [tableHeight, setTableHeight] = useState(450);
  const [loading, setLoading] = useState(true);
  const [databaseStatusLoading, setSetDatabaseStatusLoading] = useState(true);
  // 取状态接口失败时的后端错误 key，用于把提示分辨成「没配数据源」而不是「没开 profiling」
  const [statusErrKey, setStatusErrKey] = useState(null);
  const [dataSource, setDataSource] = useState([]);
  const [currentPage, setCurrentPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [filter, setFilter] = useState({
    searchValue: null,
  });
  const [manualUpdate, setManualUpdate] = useState(0);
  const [sourceFilter, setSourceFilter] = useState(null);
  // 弹窗相关
  const [detailModal, setDetailModal] = useState(false);
  const [suggestIndexModal, setSuggestIndexModal] = useState(false);
  const [createIndexModal, setCreateIndexModal] = useState(false);
  const [urlRuleModal, setUrlRuleModal] = useState(false);
  const [currentRecord, setCurrentRecord] = useState({});
  const [createIndexLoading, setCreateIndexLoading] = useState(false);
  const [showIdInfo, setShowIdInfo] = useState(false);

  // 慢查询没在工作时，到底缺什么。四种成因的处置方式完全不同，不能都说成「未开启识别策略」：
  //   ① 没有诊断数据源 —— 去数据源页勾「诊断」用途，跟 MongoDB 的 profiling 无关
  //   ② 账号没权限开 profiling —— 要去目标库手工 setProfilingLevel，改数据源没用
  //   ③ 读不到 profiling 状态 —— 通常是账号权限或目标库不可达
  //   ④ 目标库 profiling 确实关着
  const ruleHint = useMemo(() => {
    if (statusErrKey === 'slowquery_target_unconfigured') {
      return {
        text: _l('还没有指定慢查询的诊断目标库'),
        tip: _l('到「数据源」页给被监控的 MongoDB 勾上「诊断」用途即可，勾完立即生效。这一项与目标库是否开启 profiling 无关。'),
      };
    }
    if (statusErrKey) {
      return {
        text: _l('读取慢查询状态失败'),
        tip: _l('目标 MongoDB 可能不可达，或数据源里的账号权限不足。到「数据源」页对该实例点「测试连接」可看到具体原因。'),
      };
    }
    const st = databaseStatus.profileStatus?.MD_WSServiceDefault?.status;
    if (st === 'denied') {
      return {
        text: _l('目标库的慢查询记录未能自动开启'),
        tip: _l('数据源里配的账号没有开启 profiling 的权限（需要 dbAdmin）。可以换一个有权限的账号，或到目标库上手工执行 db.setProfilingLevel(1, %0)。', databaseStatus.captureSlowMS || 100),
      };
    }
    if (st === 'unknown') {
      return {
        text: _l('读不到目标库的慢查询状态'),
        tip: _l('执行 profile 命令被拒绝或超时，通常是账号权限不足（需要 dbAdmin）或目标库不可达。'),
      };
    }
    return { text: _l('未开启慢查询识别策略'), tip: '' };
  }, [statusErrKey, databaseStatus]);

  // 基础数据源真名(后端 global-args 提供)；无则回退「默认」。
  const defaultSourceLabel = databaseStatus?.defaultSourceName || _l('默认');
  // 来源归一化：与「实例名」列保持相同逻辑(MD_WSServiceDefault/空 → 基础数据源真名)
  const normalizeDbtag = (dbtag) =>
    !dbtag || dbtag === 'MD_WSServiceDefault' ? defaultSourceLabel : dbtag;

  // 来源筛选：从当前页数据中提取去重的归一化来源，多于 1 个时才显示下拉
  const sources = useMemo(
    () => [
      ...new Set(
        dataSource
          .map((r) => normalizeDbtag(r.slowQuery?.dbtag))
      ),
    ],
    [dataSource, defaultSourceLabel]
  );

  // 前端筛选：按归一化来源过滤展示数据
  const filteredDataSource = useMemo(
    () =>
      sourceFilter
        ? dataSource.filter((r) => normalizeDbtag(r.slowQuery?.dbtag) === sourceFilter)
        : dataSource,
    [dataSource, sourceFilter]
  );

  const columns = useMemo(
    () => [
      {
        width: 150,
        title: _l('查询字段'),
        // ellipsis + 纯文本 render：字段名可以很长，包在 div 里时 td 的 overflow 只会
        // 把它裁掉、不出省略号，看起来就是硬压到右边一列上。
        ellipsis: true,
        render: (text, record) => record.controlInfo?.controlname || '—',
      },
      {
        width: 250,
        title: _l('查询字段路径'),
        // 「应用名-工作表名」拼出来常常超过列宽（实测「Mongo慢查询测试-20260731-40万行
        // 慢查询数据」直接盖住相邻的查询语句列），同样要截断。
        ellipsis: true,
        // 非 HAP 基础数据库(无工作表/工作流元数据)或解析不到时，controlInfo 为空，
        // 显示「—」而非 undefined-undefined。
        render: (text, record) => {
          const { appname, worksheetname } = record.controlInfo || {};
          if (!appname && !worksheetname) return '—';
          return `${appname || '—'}-${worksheetname || '—'}`;
        },
      },
      {
        width: 250,
        title: _l('查询语句'),
        ellipsis: true,
        render: (text, record) => {
          const formattedJson = JSON.stringify(record.slowQuery, null, 2);
          return (
            <Tooltip
              overlayInnerStyle={{
                width: 420,
                height: 400,
                overflow: 'auto',
                scrollbarWidth: 'none',
              }}
              title={<pre>{formattedJson}</pre>}
            >
              <Text
                copyable={{ tooltips: false, text: formattedJson }}
                ellipsis={{ tooltip: false }}
              >
                {formattedJson}
              </Text>
            </Tooltip>
          );
        },
      },
      {
        width: 180,
        title: _l('实例名'),
        // 实例名由用户在「数据源」页自取，长度不可控，同样截断
        ellipsis: true,
        render: (text, record) => {
          const { dbtag } = record.slowQuery || {};
          if (dbtag === 'MD_WSServiceDefault' || !dbtag) return defaultSourceLabel;
          return dbtag;
        },
      },
      {
        width: 200,
        title: _l('单次查询扫描行记录数'),
        render: (text, record) =>
          `${formatNumber(record.slowQuery?.docsexamined || 0)}${_l('条')}`,
      },
      {
        width: 150,
        title: _l('慢操作出现次数'),
        render: (text, record) => `${record.slowQuery?.count || 0}${_l('次')}`,
      },
      {
        width: 180,
        title: _l('字段关联工作流个数'),
        render: (text, record) =>
          `${record.controlInfo?.processlist?.length || 0}${_l('个')}`,
      },
      {
        width: 200,
        title: _l('产生时间'),
        dataIndex: 'timestamp',
        render: (text) =>
          moment.utc(text).local().format('YYYY-MM-DD HH:mm:ss'),
      },
      {
        width: 160,
        title: _l('操作'),
        fixed: 'right',
        render: (text, record) => (
          <div>
            <span
              className="primaryTextBtn"
              onClick={() => {
                setCurrentRecord(record);
                setDetailModal(true);
              }}
            >
              {_l('详情')}
            </span>
            {renderIndexBtn(record)}
          </div>
        ),
      },
    ],
    [defaultSourceLabel]
  );

  /**
   * 渲染索引按钮
   * TaskStatus = [pending, processing, completed, failed]
   * pending: 索引已到任务队列
   * processing: 索引创建中
   * completed: 索引创建成功
   * failed: 索引创建失败
   * */
  const renderIndexBtn = (record) => {
    const { worksheetid, controlid, indexStatus } = record.controlInfo || {};
    // 可以创建索引
    if (
      worksheetid &&
      controlid &&
      (!indexStatus || ['failed', ''].includes(indexStatus?.status))
    ) {
      return (
        <span
          className="primaryTextBtn ml16"
          onClick={() => {
            setCurrentRecord(record);
            setSuggestIndexModal(true);
          }}
        >
          {_l('建议索引')}
        </span>
      );
    }
    // 索引创建中
    if (
      indexStatus?.status === 'pending' ||
      indexStatus?.status === 'processing'
    ) {
      return (
        <Tooltip
          title={_l('索引正在创建中，预计需要几秒到几十秒，请耐心等待。')}
        >
          <span className="disabledTextBtn ml16">{_l('索引创建中...')}</span>
        </Tooltip>
      );
    }
    // 索引按钮禁用
    return <span className="disabledTextBtn ml16">{_l('建议索引')}</span>;
  };

  // 分页
  const handlePaginationChange = (page) => {
    if (page === currentPage) return;
    setCurrentPage(page);
  };

  // 切换数据库
  const handleDatabaseChange = (value) => {
    setCurrentDatabase(value);
  };

  // 查询
  const handleSearch = () => {
    setFilter({
      ...filter,
      searchValue: searchValue,
    });
    setCurrentPage(1);
  };

  // 刷新
  const handleRefresh = () => {
    setLoading(true);
    executeTask().then(() => {
      if (currentPage != 1) setCurrentPage(1);
      else setManualUpdate(manualUpdate + 1);
    });
  };

  // 创建索引
  const handleCreateIndex = () => {
    setCreateIndexLoading(true);
    createIndex({
      dbname: currentDatabase,
      worksheetID: currentRecord.controlInfo?.worksheetid,
      controlID: currentRecord.controlInfo?.controlid,
      dbTag: currentRecord.slowQuery?.dbtag,
    })
      .then(() => {
        message.success(_l('操作成功！'));
        let timer = setTimeout(() => {
          setCreateIndexModal(false);
          setSuggestIndexModal(false);
          setManualUpdate(manualUpdate + 1);
          clearTimeout(timer);
        }, 1000);
      })
      .finally(() => {
        setCreateIndexLoading(false);
      });
  };

  // 获取建议索引文本
  const getSuggestIndexText = () => {
    const { worksheetid, controlid } = currentRecord.controlInfo || {};
    return `db.ws${worksheetid}.createIndex({"${controlid}": 1}`;
  };

  // 启动定时器
  const startTimer = () => {
    clearTimer(); // 确保旧定时器被清除
    timerRef.current = setInterval(() => {
      setManualUpdate(manualUpdate + 1); // 每隔 5 秒获取一次数据
    }, 5000);
  };

  // 清除定时器
  const clearTimer = () => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  };

  // 动态计算表格高度
  useEffect(() => {
    const calculateHeight = _.debounce(() => {
      const vh = window.innerHeight;
      const availableHeight = vh - 430;
      setTableHeight(Math.max(400, availableHeight));
    }, 200);

    calculateHeight();
    window.addEventListener('resize', calculateHeight);

    return () => {
      window.removeEventListener('resize', calculateHeight);
    };
  }, []);

  useEffect(() => {
    setLoading(true);
    setSetDatabaseStatusLoading(true);
    setStatusErrKey(null);
    getSlowQueryDetail()
      .then(({ data }) => {
        setDatabaseStatus(data || {});
        // 如果页码不是1，则设置为1，否则手动更新
        if (currentPage != 1) setCurrentPage(1);
        else setManualUpdate(manualUpdate + 1);
      })
      .catch((e) => {
        // 接口失败(最常见是 503 slowquery_target_unconfigured)时留下错误 key，
        // 让下面的提示能说清到底缺什么 —— 早先这里没有 catch，任何失败都会退化成
        // 一句「未开启慢查询识别策略」，把「没配诊断数据源」误导成「去 MongoDB 开 profiling」。
        setDatabaseStatus({});
        setStatusErrKey(e?.response?.data?.key || e?.data?.key || 'unknown');
      })
      .finally(() => {
        setSetDatabaseStatusLoading(false);
      });
  }, [currentDatabase]);

  useEffect(() => {
    // all = profiling level 2(记录所有查询)，system.profile 照样有数据，不该被挡在外面
    const profStatus = databaseStatus.profileStatus?.MD_WSServiceDefault?.status;
    if (profStatus === 'slow' || profStatus === 'all') {
      setLoading(true);
      getSlowQueryList({
        page: currentPage,
        pageSize: 10,
        searchText: filter.searchValue,
      })
        .then(({ data }) => {
          setTotal(data.count);
          setDataSource(data.data || []);
          // 判断是否有索引创建中的数据，如果有则添加定时器轮询
          let filterCreateIndex = data?.data?.filter((item) =>
            ['pending', 'processing'].includes(
              item.controlInfo?.indexStatus?.status
            )
          );
          if (filterCreateIndex?.length) startTimer();
          else clearTimer();
        })
        .finally(() => {
          setLoading(false);
        });
    } else {
      setDataSource([]);
      setTotal(0);
      setLoading(false);
    }

    return () => {
      clearTimer();
    };
  }, [manualUpdate, currentPage, filter.searchValue]);

  return (
    <div className="slowQueryContainer">
      <div className="slowQueryToolbar">
        <div className="slowQueryToolbarHeader">
          <div className="databaseContainer">
            <Select
              className="databaseSelect"
              prefix={<DatabaseOutlined />}
              defaultValue="mdwsrows"
              onChange={handleDatabaseChange}
              options={[{ value: 'mdwsrows', label: 'mdwsrows' }]}
            />
            <div className="slowQueryRule">
              {databaseStatusLoading ? (
                <Spin size="small" />
              ) : databaseStatus.profileStatus?.MD_WSServiceDefault?.status ===
                'slow' ? (
                <>
                  <span className="slowQueryRuleStatus"></span>
                  <span>
                    {_l('执行时长 >')}{' '}
                    <strong>
                      {databaseStatus.profileStatus?.MD_WSServiceDefault?.slowms}{' '}
                      ms
                    </strong>
                    、{_l('扫描文档数 >')}{' '}
                    <strong>
                      {formatNumber(databaseStatus.docsExamined || 0)}
                    </strong>
                    、{_l('且同类查询在窗口内出现 ≥')}{' '}
                    <strong>{databaseStatus.count || 50} {_l('次')}</strong>
                    {_l('，才会被记录')}
                    <Tooltip
                      title={_l(
                        '四个条件要同时满足：① 执行时长超过 slowms；② 扫描文档数超过阈值（查询慢但扫得少不算）；③ 同一查询指纹在最近 120 分钟内出现次数达标；④ 平台每 %0 分钟采集一次，刚触发完需要等下一个周期。阈值可用 ENV_GATEWAY_DOCS_EXAMINED / ENV_GATEWAY_CAPTURE_INTERVAL_MS / ENV_GATEWAY_PROFILE_SLOW_MS 调整。',
                        Math.round((databaseStatus.captureIntervalMS || 600000) / 60000),
                      )}
                    >
                      <QuestionCircleOutlined style={{ marginLeft: 6, cursor: 'help' }} />
                    </Tooltip>
                  </span>
                </>
              ) : (
                <>
                  <ExclamationCircleOutlined className="slowQueryRuleWarning" />
                  <span>{ruleHint.text}</span>
                  {ruleHint.tip && (
                    <Tooltip title={ruleHint.tip}>
                      <QuestionCircleOutlined style={{ marginLeft: 6, cursor: 'help' }} />
                    </Tooltip>
                  )}
                </>
              )}
            </div>
          </div>
          <div className="slowQueryToolbarActions">
            {!databaseStatusLoading && (
              <Tooltip
                title={`${_l(
                  '呈现最近 %0内的慢查询语句，默认每 %1自动刷新一次。',
                  formatTime(databaseStatus?.logExpireMS || 0),
                  formatTime(databaseStatus?.captureIntervalMS || 0)
                )}`}
              >
                <Button
                  type="text"
                  icon={<ExclamationCircleOutlined />}
                  aria-label={_l('说明')}
                />
              </Tooltip>
            )}
            <Tooltip title={_l('立即刷新')}>
              <Button
                icon={<ReloadOutlined />}
                onClick={handleRefresh}
                aria-label={_l('立即刷新')}
              />
            </Tooltip>
          </div>
        </div>
        <div className="slowQueryFilters">
          <Input
            className="slowQuerySearch"
            value={searchValue}
            placeholder={_l('全文档搜索')}
            prefix={<SearchOutlined />}
            allowClear
            onChange={(e) => setSearchValue(e.target.value)}
            onPressEnter={handleSearch}
          />
          {sources.length > 1 && (
            <Select
              className="sourceSelect"
              value={sourceFilter}
              allowClear
              placeholder={_l('全部来源')}
              onChange={(val) => setSourceFilter(val ?? null)}
              options={sources.map((s) => ({ value: s, label: s }))}
            />
          )}
        </div>
      </div>
      <div className="tableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={filteredDataSource}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  <div style={{ textAlign: 'left', maxWidth: 560, margin: '0 auto' }}>
                    <div style={{ marginBottom: 8 }}>{_l('暂无慢查询记录。没抓到通常是下面某个条件没满足：')}</div>
                    <ol style={{ paddingLeft: 20, lineHeight: 1.9, margin: 0 }}>
                      <li>
                        {_l('慢查询识别策略：')}
                        {databaseStatus.profileStatus?.MD_WSServiceDefault?.status === 'slow'
                          ? _l('已开启（slowms %0 ms）', databaseStatus.profileStatus?.MD_WSServiceDefault?.slowms)
                          : databaseStatus.profileStatus?.MD_WSServiceDefault?.status === 'all'
                            ? _l('已开启（记录所有查询）')
                            : ruleHint.text}
                      </li>
                      <li>{_l('扫描文档数要大于 %0，「查询慢但扫得少」不算', formatNumber(databaseStatus.docsExamined || 0))}</li>
                      <li>{_l('同一查询指纹在最近 120 分钟内要出现 ≥ %0 次，跑一两次不算', databaseStatus.count || 50)}</li>
                      <li>{_l('平台每 %0 分钟采集一次，刚触发完请等下一个周期再看', Math.round((databaseStatus.captureIntervalMS || 600000) / 60000))}</li>
                    </ol>
                    <div style={{ marginTop: 8, color: 'var(--color-text-secondary, #757575)' }}>
                      {_l('想更容易复现，可调小 ENV_GATEWAY_DOCS_EXAMINED 与 ENV_GATEWAY_CAPTURE_INTERVAL_MS。')}
                    </div>
                  </div>
                }
              />
            ),
          }}
          pagination={false}
          scroll={{ x: '100%', y: tableHeight }}
          rowKey="_id"
        />
        <Pagination
          current={currentPage}
          total={total}
          onChange={handlePaginationChange}
        />
      </div>
      {/* 详情 */}
      <Modal
        title={
          <div style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
            {_l('字段及工作流详情')}
            <Tooltip
              title={_l(
                '显示的信息不意味着工作流直接导致了慢查询。请根据提供的信息排查引用到字段的功能，并据此优化'
              )}
            >
              <ExclamationCircleOutlined
                style={{
                  color: '#999999',
                  cursor: 'pointer',
                  fontSize: '14px',
                }}
              />
            </Tooltip>
          </div>
        }
        width={800}
        open={detailModal}
        maskClosable={false}
        onCancel={() => setDetailModal(false)}
        footer={null}
      >
        <div className="slowQueryDetail">
          <div className="flexCenter mb20 grey33 font13">
            <div className="nowrap">
              {_l('当前页面显示慢查询语句的查询字段及关联工作流')}
            </div>
            <div
              className="primaryTextBtn ml6"
              onClick={() => setUrlRuleModal(true)}
            >
              {_l('查看跳转URL规则')}
            </div>
          </div>
          <div className="mb30 wordBreakAll">
            <div className="flexCenter justifySpaceBetween mb8 weight500 font14">
              <span className="font12 grey75 weight500">{_l('字段')}</span>
              <div
                className="slowQueryShowIdInfo commonTextBtnWithLight"
                onClick={() => setShowIdInfo(!showIdInfo)}
              >
                {showIdInfo ? _l('隐藏ID信息') : _l('显示ID信息')}
              </div>
            </div>
            <div className="appDetailBox">
              <div className="mb8 grey15 weight500">
                {currentRecord.controlInfo?.controlname}
                {/* {showIdInfo && `(${currentRecord.controlInfo?.controlid})`} */}
              </div>
              <div className="mb8">
                {_l('所在组织ID')}：{currentRecord.controlInfo?.projectid}
              </div>
              <div className="mb8">
                {_l('所在应用')}：{currentRecord.controlInfo?.appname}
                {showIdInfo && `(${currentRecord.controlInfo?.appid})`}
              </div>
              <div className="mb8">
                {_l('所在工作表')}：{currentRecord.controlInfo?.worksheetname}
                {showIdInfo && `(${currentRecord.controlInfo?.worksheetid})`}
              </div>

              <div className="mt30 mb8 font12 grey75 weight500">
                {_l('关联工作流')}
              </div>
              {currentRecord.controlInfo?.processlist?.length > 0 ? (
                currentRecord.controlInfo?.processlist.map((process) => {
                  return (
                    <div className="mb20 font13" key={process.processid}>
                      <div className="mb8 grey15 weight500">
                        {process.processname}
                        {showIdInfo && `(${process.processid})`}
                      </div>
                      {process.flownodes?.length > 0 &&
                        process.flownodes.map((node) => (
                          <div className="font13" key={node._id}>
                            {_l('节点')}: {node.name}
                            {showIdInfo && `(${node._id})`}
                          </div>
                        ))}
                    </div>
                  );
                })
              ) : (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={_l('暂无数据')}
                />
              )}
            </div>
          </div>
        </div>
      </Modal>
      {/* 建议索引 */}
      <Modal
        title={_l('建议索引')}
        width={700}
        open={suggestIndexModal}
        maskClosable={false}
        onCancel={() => setSuggestIndexModal(false)}
        footer={[
          <div key="copy" className="suggestIndexModalCopyBox mr22">
            <Text
              copyable={{
                text: getSuggestIndexText(),
                tooltips: false,
                icon: [<span key="copy">{_l('复制')}</span>],
              }}
            />
          </div>,
          <span
            key="createIndex"
            className="primaryTextBtn"
            onClick={() => setCreateIndexModal(true)}
          >
            {_l('创建索引')}
          </span>,
        ]}
      >
        <div className="mb8 font33">
          {_l('此处提供了自动生成的建议索引。你可以直接创建索引。')}
        </div>
        <div className="suggestIndexModalContent">{getSuggestIndexText()}</div>
      </Modal>
      {/* 创建索引二次确认 */}
      <Modal
        title={_l('创建索引')}
        open={createIndexModal}
        maskClosable={false}
        onCancel={() => setCreateIndexModal(false)}
        footer={[
          <span
            key="cancel"
            className="commonTextBtn mr44"
            onClick={() => setCreateIndexModal(false)}
          >
            {_l('取消')}
          </span>,
          <Button
            key="confirm"
            type="primary"
            loading={createIndexLoading}
            onClick={handleCreateIndex}
          >
            {_l('创建')}
          </Button>,
        ]}
      >
        <div className="createIndexModalContent">
          {_l('创建操作不可撤回，请谨慎操作')}
        </div>
      </Modal>
      {/* 跳转URL规则 */}
      <Modal
        title={_l('跳转URL规则')}
        width={800}
        open={urlRuleModal}
        maskClosable={false}
        onCancel={() => setUrlRuleModal(false)}
        footer={null}
      >
        <div className="font13 grey15 wordBreakAll">
          <div className="mb30">
            {_l('根据规则和提供的ID，可以拼接出跳转链接')}
          </div>
          <div className="mb8 weight500">
            {_l('跳转工作表URL的规则：访问地址')}
            {`/app/${_l('应用ID')}/${_l('分组ID')}/${_l('工作表ID')}`}
          </div>
          <div className="mb30">
            {_l('示例：')}
            <span className="underline">
              {
                'https://hap.domain.com/app/123e4567-e89b-12d3-a456-426614174000/4567e89b-12d3-a456-426614174000/e89b12d3-a456-426614174000?flag=1234567890123'
              }
            </span>
          </div>
          <div className="mb8 weight500">
            {_l('跳转工作流URL的规则：')}
            {_l('访问地址')}
            {`/workflowedit/${_l('工作流ID')}`}
          </div>
          <div>
            {_l('示例：')}
            <span className="underline">
              {'https://hap.domain.com/workflowedit/66f37704b8f3c82abc3a4e63'}
            </span>
          </div>
        </div>
      </Modal>
    </div>
  );
};

export default SlowQuery;
