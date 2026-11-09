import React, { useEffect, useRef, useState } from 'react';
import { EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Empty, Form, Input, message,Modal, Popconfirm, Segmented, Select, Space, Spin } from 'antd';

import { deleteMonitorPanel, listMonitorJobs, listMonitorMetrics, listMonitorPanels, runMonitorQuery, saveMonitorPanel } from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

import QueryResult from './QueryResult';

import './index.less';

const emptyPanel = { name: '', promql: '', view: 'chart' };
const defaultRange = 3600;

const query = (panel, range, signal) => runMonitorQuery(
  { promql: panel.promql, rangeSeconds: panel.view === 'chart' ? range : 0 },
  signal,
);

function PanelEditor({ panel, range, onSave, onClose }) {
  const [form] = Form.useForm();
  const [job, setJob] = useState('');
  const [jobs, setJobs] = useState([]);
  const [search, setSearch] = useState('');
  const [metrics, setMetrics] = useState([]);
  const [metricsError, setMetricsError] = useState('');
  const [preview, setPreview] = useState(null);
  const [saving, setSaving] = useState(false);
  const previewRequest = useRef(null);

  useEffect(() => {
    const controller = new AbortController();
    listMonitorJobs(controller.signal)
      .then(({ data }) => {
        if (!controller.signal.aborted && Array.isArray(data)) setJobs(data);
      })
      .catch(() => {
        // Metric search remains available across all jobs if job discovery fails.
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const timer = setTimeout(async () => {
      try {
        const { data } = await listMonitorMetrics({ search, job }, controller.signal);
        if (!controller.signal.aborted) {
          setMetrics(Array.isArray(data) ? data.slice(0, 200) : []);
          setMetricsError('');
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setMetrics([]);
          setMetricsError(apiErr(error, _l('加载指标失败')));
        }
      }
    }, 200);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [job, search]);

  useEffect(() => () => previewRequest.current?.abort(), []);

  const resetPreview = () => {
    previewRequest.current?.abort();
    setPreview(null);
  };

  const selectMetric = (metric) => {
    const labels = [];
    const bareName = /^[a-zA-Z_:][a-zA-Z0-9_:]*$/.test(metric);
    if (!bareName) labels.push(`__name__=${JSON.stringify(metric)}`);
    if (job) labels.push(`job=${JSON.stringify(job)}`);
    const expression = `${bareName ? metric : ''}${labels.length ? `{${labels.join(',')}}` : ''}`;
    form.setFieldValue('promql', expression);
    setSearch('');
    resetPreview();
  };

  const previewQuery = async () => {
    const values = await form.validateFields(['promql', 'view']).catch(() => null);
    if (!values) return;
    resetPreview();
    const controller = new AbortController();
    previewRequest.current = controller;
    setPreview({ loading: true });
    try {
      const { data } = await query(values, range, controller.signal);
      if (!controller.signal.aborted) setPreview({ data, view: values.view });
    } catch (error) {
      if (!controller.signal.aborted) setPreview({ error: apiErr(error, _l('查询失败')) });
    }
  };

  const save = async (values) => {
    if (saving) return;
    setSaving(true);
    try {
      const { data } = await saveMonitorPanel(panel.id, values);
      onSave(data);
    } catch (error) {
      message.error(apiErr(error, _l('保存失败')));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={panel.id ? _l('编辑监控面板') : _l('新建监控面板')}
      open
      width={780}
      onOk={() => form.submit()}
      onCancel={onClose}
      okText={_l('保存')}
      cancelText={_l('取消')}
      confirmLoading={saving}
      cancelButtonProps={{ disabled: saving }}
      closable={!saving}
      maskClosable={!saving}
    >
      <Form form={form} initialValues={panel} layout="vertical" onFinish={save} onValuesChange={resetPreview}>
        <Form.Item name="name" label={_l('面板名称')} rules={[{ required: true, whitespace: true }]}>
          <Input maxLength={80} />
        </Form.Item>
        <Form.Item name="view" label={_l('显示方式')}>
          <Segmented options={[{ label: _l('时间曲线'), value: 'chart' }, { label: _l('即时表格'), value: 'table' }]} />
        </Form.Item>
        <div className="monitorMetricPicker">
          <Select
            aria-label={_l('采集任务')}
            value={job}
            options={[{ value: '', label: _l('全部采集任务') }, ...jobs.map((value) => ({ value, label: value }))]}
            onChange={(value) => { setJob(value); setMetrics([]); }}
          />
          <Select
            aria-label={_l('搜索并填入指标')}
            value={null}
            showSearch
            filterOption={false}
            searchValue={search}
            placeholder={_l('搜索并填入指标')}
            options={metrics.map((value) => ({ value, label: value }))}
            notFoundContent={metricsError || _l('没有找到指标')}
            onSearch={setSearch}
            onChange={selectMetric}
          />
        </div>
        <Form.Item name="promql" label="PromQL" rules={[{ required: true, whitespace: true }]}>
          <Input.TextArea className="monitorExpression" autoSize={{ minRows: 3, maxRows: 8 }} maxLength={4096} placeholder='mongodb_up{job="mongodb"}' />
        </Form.Item>
        <Button onClick={previewQuery} loading={preview?.loading}>{_l('预览')}</Button>
        {preview?.error && <Alert type="error" showIcon message={preview.error} />}
        {preview?.data && <QueryResult data={preview.data} view={preview.view} />}
      </Form>
    </Modal>
  );
}

export default function MonitorPanels() {
  const [panels, setPanels] = useState(null);
  const [error, setError] = useState('');
  const [results, setResults] = useState({});
  const [range, setRange] = useState(defaultRange);
  const [revision, setRevision] = useState(0);
  const [refreshSeconds, setRefreshSeconds] = useState(30);
  const [editing, setEditing] = useState(null);

  useEffect(() => {
    const controller = new AbortController();
    listMonitorPanels(controller.signal)
      .then(({ data }) => {
        if (!controller.signal.aborted) {
          setError('');
          setPanels(data);
        }
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(apiErr(err, _l('加载监控面板失败')));
      });
    return () => controller.abort();
  }, [revision]);

  useEffect(() => {
    if (!panels?.length) return;
    const controller = new AbortController();
    let timer;
    setResults({});
    const run = async () => {
      for (let index = 0; index < panels.length && !controller.signal.aborted; index += 3) {
        await Promise.all(panels.slice(index, index + 3).map(async (panel) => {
          let result;
          try {
            const { data } = await query(panel, range, controller.signal);
            result = { data };
          } catch (err) {
            result = { error: apiErr(err, _l('查询失败')) };
          }
          if (!controller.signal.aborted) setResults((current) => ({ ...current, [panel.id]: result }));
        }));
      }
      if (refreshSeconds && !controller.signal.aborted) {
        timer = setTimeout(run, refreshSeconds * 1000);
      }
    };
    run();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [panels, range, refreshSeconds]);

  const remove = async (id) => {
    try {
      await deleteMonitorPanel(id);
      setPanels((current) => current.filter((panel) => panel.id !== id));
    } catch (err) {
      message.error(apiErr(err, _l('删除失败')));
    }
  };

  const onSave = (panel) => {
    setPanels((current) => current.some((item) => item.id === panel.id)
      ? current.map((item) => item.id === panel.id ? panel : item)
      : [...current, panel]);
    setEditing(null);
  };

  return (
    <section className="monitorPanels">
      <header className="monitorToolbar">
        <h1>{_l('自定义监控')}</h1>
        <Space wrap>
          <Select
            aria-label={_l('时间范围')}
            value={range}
            onChange={setRange}
            options={[
              { value: 3600, label: _l('最近 1 小时') },
              { value: 21600, label: _l('最近 6 小时') },
              { value: 86400, label: _l('最近 24 小时') },
              { value: 604800, label: _l('最近 7 天') },
            ]}
          />
          <Select
            aria-label={_l('自动刷新')}
            value={refreshSeconds}
            onChange={setRefreshSeconds}
            options={[
              { value: 0, label: _l('手动刷新') },
              { value: 30, label: _l('每 30 秒刷新') },
              { value: 60, label: _l('每 60 秒刷新') },
            ]}
          />
          <Button icon={<ReloadOutlined />} onClick={() => setRevision((value) => value + 1)}>{_l('刷新')}</Button>
          <Button type="primary" icon={<PlusOutlined />} disabled={!panels} onClick={() => setEditing(emptyPanel)}>{_l('新建监控面板')}</Button>
        </Space>
      </header>
      {error && <Alert type="error" showIcon message={error} />}
      {!panels && !error && <Spin />}
      {panels?.length === 0 && <Empty description={_l('还没有监控面板')} />}
      <div className="monitorGrid">
        {panels?.map((panel) => (
          <Card key={panel.id} size="small" title={panel.name} extra={
            <Space>
              <Button type="text" size="small" icon={<EditOutlined />} onClick={() => setEditing(panel)}>{_l('编辑')}</Button>
              <Popconfirm
                title={_l('删除此监控面板？')}
                okText={_l('删除')}
                okType="danger"
                cancelText={_l('取消')}
                onConfirm={() => remove(panel.id)}
              >
                <Button type="text" size="small" danger>{_l('删除')}</Button>
              </Popconfirm>
            </Space>
          }>
            <code className="monitorPanelExpression" title={panel.promql}>{panel.promql}</code>
            {!results[panel.id] ? <Spin /> : results[panel.id].error ? (
              <Alert type="error" showIcon message={results[panel.id].error} />
            ) : (
              <QueryResult data={results[panel.id].data} view={panel.view} />
            )}
          </Card>
        ))}
      </div>
      {editing && <PanelEditor panel={editing} range={range} onClose={() => setEditing(null)} onSave={onSave} />}
    </section>
  );
}
