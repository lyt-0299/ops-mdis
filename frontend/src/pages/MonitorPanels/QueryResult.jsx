import React from 'react';
import { Alert, Empty, Table } from 'antd';

import { _l } from '@/utils/utils';

const formatTime = (time) => new Date(time * 1000).toLocaleString();
const formatValue = (value) => Number(value).toLocaleString(undefined, { maximumSignificantDigits: 6 });

const seriesName = (labels = {}) => {
  const { __name__: name, ...tags } = labels;
  const entries = Object.entries(tags).sort(([a], [b]) => a.localeCompare(b));
  if (!entries.length) return name || _l('无标签');
  return `${name || ''}{${entries.map(([key, value]) => `${key}=${JSON.stringify(value)}`).join(', ')}}`;
};

function histogramText(histogram) {
  return `count=${histogram.count}, sum=${histogram.sum}`;
}

function latestRows(data) {
  if (data.resultType === 'scalar' || data.resultType === 'string') {
    return [{ key: 'single', name: data.resultType, value: data.result[1], time: data.result[0] }];
  }
  return data.result.flatMap((series, key) => {
    const sample = series.value || series.values?.at(-1);
    const histogram = series.histogram || series.histograms?.at(-1);
    const useHistogram = histogram && (!sample || histogram[0] > sample[0]);
    const last = useHistogram ? histogram : sample;
    return last ? [{ key, name: seriesName(series.metric), time: last[0], value: useHistogram ? histogramText(last[1]) : last[1] }] : [];
  });
}

function TimeSeries({ series, start, end, step }) {
  const lines = series.map((item) => ({
    name: seriesName(item.metric),
    points: (item.values || []).map(([time, value]) => [time, Number(value)]),
  }));
  const points = lines.flatMap((line) => line.points).filter(([, value]) => Number.isFinite(value));
  if (!points.length) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={_l('曲线需要数值样本，可切换表格查看结果')} />;
  }

  const minTime = start ?? Math.min(...points.map(([time]) => time));
  const maxTime = end ?? Math.max(...points.map(([time]) => time));
  const minValue = Math.min(...points.map(([, value]) => value));
  const maxValue = Math.max(...points.map(([, value]) => value));
  const width = 760;
  const height = 210;
  const left = 72;
  const right = width - 24;
  const top = 14;
  const bottom = height - 32;
  const x = (time) => minTime === maxTime ? (left + right) / 2 : left + (time - minTime) * (right - left) / (maxTime - minTime);
  const y = (value) => minValue === maxValue ? (top + bottom) / 2 : bottom - (value - minValue) * (bottom - top) / (maxValue - minValue);
  const dashes = ['', '7 3', '2 3', '10 3 2 3'];
  const hasHistograms = series.some((item) => item.histograms?.length);

  return (
    <>
      {hasHistograms && <Alert type="info" showIcon message={_l('直方图样本可切换表格查看')} />}
      <svg className="monitorChart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={_l('指标时间曲线')}>
        {[top, (top + bottom) / 2, bottom].map((lineY, index) => (
          <g key={lineY}>
            <line className="monitorGridLine" x1={left} x2={right} y1={lineY} y2={lineY} />
            <text x={left - 7} y={lineY + 4} textAnchor="end">{formatValue(maxValue - (maxValue - minValue) * index / 2)}</text>
          </g>
        ))}
        <text x={left} y={height - 6}>{formatTime(minTime)}</text>
        {maxTime !== minTime && <text x={right} y={height - 6} textAnchor="end">{formatTime(maxTime)}</text>}
        {lines.map(({ points: samples }, index) => {
          let previousTime = null;
          const path = samples.map(([time, value]) => {
            if (!Number.isFinite(value)) {
              previousTime = null;
              return '';
            }
            const command = previousTime !== null && (!step || time - previousTime <= step * 1.5) ? 'L' : 'M';
            previousTime = time;
            return `${command}${x(time)},${y(value)}`;
          }).join(' ');
          return (
            <g key={index} opacity={1 - (index % 5) * 0.13}>
              <path className="monitorSeries" d={path} strokeDasharray={dashes[index % dashes.length]} />
              {samples.filter(([, value]) => Number.isFinite(value)).slice(-1).map(([time, value]) => (
                <circle className="monitorSample" key={time} cx={x(time)} cy={y(value)} r={3} />
              ))}
            </g>
          );
        })}
      </svg>
      <ol className="monitorLegend">
        {lines.map(({ name }, index) => (
          <li key={name} title={name}>
            <svg width="24" height="10" aria-hidden="true">
              <line className="monitorSeries" x1="0" x2="24" y1="5" y2="5" strokeDasharray={dashes[index % dashes.length]} opacity={1 - (index % 5) * 0.13} />
            </svg>
            <span>{name}</span>
          </li>
        ))}
      </ol>
    </>
  );
}

export default function QueryResult({ data, view }) {
  if (!data?.result?.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={_l('所选范围内没有匹配的指标数据')} />;
  const limit = view === 'chart' ? 20 : 100;
  return (
    <>
      {data.truncated && <Alert type="info" showIcon message={_l('仅显示前 %0 个序列，请缩小查询范围', limit)} />}
      {view === 'chart' ? (
        <TimeSeries series={data.resultType === 'matrix' ? data.result : []} start={data.start} end={data.end} step={data.step} />
      ) : (
        <Table
          size="small"
          rowKey="key"
          dataSource={latestRows(data)}
          pagination={{ pageSize: 8, hideOnSinglePage: true, showSizeChanger: false }}
          columns={[
            { title: _l('指标及标签'), dataIndex: 'name', ellipsis: true },
            { title: _l('值'), dataIndex: 'value' },
            { title: _l('采样时间'), dataIndex: 'time', render: formatTime },
          ]}
        />
      )}
    </>
  );
}
