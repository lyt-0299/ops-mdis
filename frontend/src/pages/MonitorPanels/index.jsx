import React, { useEffect, useState } from 'react';
import { ExportOutlined } from '@ant-design/icons';
import { Alert, Button, Spin } from 'antd';

import { openMonitorDashboard } from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

import './index.less';

export default function MonitorPanels() {
  const [dashboard, setDashboard] = useState(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    openMonitorDashboard(controller.signal)
      .then(({ data }) => {
        if (!controller.signal.aborted) setDashboard(data.url);
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(apiErr(err, _l('打开 Grafana 看板失败')));
      });
    return () => controller.abort();
  }, [attempt]);

  const retry = () => {
    setError('');
    setAttempt((value) => value + 1);
  };

  return (
    <section className="monitorPanels">
      <header className="monitorToolbar">
        <h1>{_l('自定义监控')}</h1>
        {dashboard && (
          <Button icon={<ExportOutlined />} href={dashboard} target="_blank" rel="noopener noreferrer">
            {_l('在 Grafana 中打开')}
          </Button>
        )}
      </header>
      {dashboard && <iframe className="monitorGrafanaFrame" src={dashboard} title={_l('自定义监控')} />}
      {!dashboard && !error && <Spin />}
      {error && (
        <Alert
          type="error"
          showIcon
          message={error}
          action={<Button size="small" onClick={retry}>{_l('重试加载')}</Button>}
        />
      )}
    </section>
  );
}
