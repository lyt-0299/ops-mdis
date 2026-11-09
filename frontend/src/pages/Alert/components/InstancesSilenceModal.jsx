/**
 * InstancesSilenceModal — 实例明细与静默 dialog.
 *
 * Props:
 *   open              {boolean}
 *   onClose           {Function}
 *   title             {string}    Modal title (e.g. rule name)
 *   objectLabel       {string}    "实例" | "主机" etc., default "实例"
 *   instances         {Array}     list of instance state objects
 *   loading           {boolean}   initial load spinner
 *   refreshing        {boolean}   refresh-button spinner
 *   onRefresh         {Function}  () => void
 *   onCreateSilence   {Function}  (payload) => Promise<boolean>
 *                                 payload: { scope_keys, mode, muted_until?, reason }
 *   onCancelSilence   {Function}  (instance) => Promise<boolean>
 *   formatValue       {Function}  (instance) => string  — e.g. "85.30%"
 *   emptyText         {string}    shown when instances is empty
 */
import React, { useState } from 'react';
import { SyncOutlined } from '@ant-design/icons';
import {
  Button,
  Col,
  DatePicker,
  Input,
  message,
  Modal,
  Row,
  Space,
  Statistic,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import dayjs from 'dayjs';

import { _l } from '@/utils/utils';

const { TextArea } = Input;

const STATE_TAG = {
  OK:        { color: 'success',    text: 'OK' },
  Pending:   { color: 'warning',    text: _l('预警') },
  Firing:    { color: 'error',      text: _l('告警') },
  Recovered: { color: 'processing', text: _l('恢复') },
};

function formatTime(iso) {
  if (!iso) return '-';
  try {
    const d = new Date(iso);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  } catch { return iso; }
}

function formatMutedUntil(v) {
  if (!v) return _l('永久');
  return formatTime(v);
}

function formatIdentity(inst) {
  const displayName = (inst.displayName || '').trim();
  const scopeKey    = (inst.scopeKey || '').trim();
  const match = displayName.match(/^(.+?)\s*\((.+)\)$/);
  if (match) return { primary: match[2].trim(), hostname: match[1].trim() };
  const primary  = scopeKey || displayName;
  const hostname = displayName && displayName !== scopeKey && displayName !== primary ? displayName : '';
  return { primary, hostname };
}

const SILENCE_PRESETS = [
  { label: _l('1 小时'),  value: '1h',        hours: 1 },
  { label: _l('6 小时'),  value: '6h',        hours: 6 },
  { label: _l('24 小时'), value: '24h',       hours: 24 },
  { label: _l('自定义'),  value: 'custom',    hours: null },
  { label: _l('永久'),    value: 'permanent', hours: null },
];

const InstancesSilenceModal = ({
  open,
  onClose,
  title,
  objectLabel = _l('实例'),
  instances = [],
  loading,
  refreshing,
  onRefresh,
  onCreateSilence,
  onCancelSilence,
  formatValue,
  emptyText,
}) => {
  const [silenceTarget,  setSilenceTarget]  = useState(null);
  const [silencePreset,  setSilencePreset]  = useState('1h');
  const [silenceUntil,   setSilenceUntil]   = useState(null);
  const [silenceReason,  setSilenceReason]  = useState('');
  const [submitting,     setSubmitting]     = useState(false);

  const openSilence = (inst) => {
    setSilenceTarget(inst);
    setSilencePreset('1h');
    setSilenceUntil(null);
    setSilenceReason('');
  };

  const closeSilence = () => setSilenceTarget(null);

  const getMutedUntil = () => {
    if (silencePreset === 'permanent') return null;
    if (silencePreset === 'custom') {
      if (!silenceUntil) return '';
      return silenceUntil.toISOString();
    }
    const preset = SILENCE_PRESETS.find((p) => p.value === silencePreset);
    if (!preset?.hours) return null;
    return new Date(Date.now() + preset.hours * 3600 * 1000).toISOString();
  };

  const submitSilence = async () => {
    if (!silenceTarget) return;
    const mutedUntil = getMutedUntil();
    const mode = silencePreset === 'permanent' ? 'permanent' : 'temporary';
    if (mode === 'temporary' && !mutedUntil) {
      message.error(_l('请选择静默到期时间'));
      return;
    }
    setSubmitting(true);
    const ok = await onCreateSilence({
      scopeKeys: [silenceTarget.scopeKey],  // backend AlertSilenceRequest uses snake_case scope_keys
      mode,
      mutedUntil: mutedUntil || undefined,  // backend AlertSilenceRequest uses snake_case muted_until
      reason: silenceReason.trim(),
    });
    setSubmitting(false);
    if (ok) closeSilence();
  };

  // Summary stats
  const total          = instances.length;
  const firingUnmuted  = instances.filter((i) => i.alertState === 'Firing' && !i.muted).length;
  const firingMuted    = instances.filter((i) => i.alertState === 'Firing' && i.muted).length;
  const normalCount    = instances.filter((i) => i.alertState !== 'Firing').length;

  const columns = [
    {
      title: objectLabel,
      dataIndex: 'scopeKey',
      ellipsis: true,
      render: (_, inst) => {
        const id = formatIdentity(inst);
        return (
          <div>
            <div style={{ fontFamily: 'monospace', fontWeight: 600, color: '#222' }}>{id.primary}</div>
            {id.hostname && (
              <div style={{ fontSize: 11, color: '#999', marginTop: 2 }}>
                {_l('主机名')}：{id.hostname}
              </div>
            )}
          </div>
        );
      },
    },
    {
      title: _l('当前值'),
      width: 110,
      align: 'right',
      render: (_, inst) => (
        <span style={{ fontFamily: 'monospace', fontSize: 12 }}>
          {formatValue ? formatValue(inst) : '-'}
        </span>
      ),
    },
    {
      title: _l('状态'),
      width: 90,
      render: (_, inst) => {
        const cfg = STATE_TAG[inst.alertState] || { color: 'default', text: inst.alertState };
        return <Tag color={cfg.color}>{cfg.text}</Tag>;
      },
    },
    {
      title: _l('静默截止'),
      width: 170,
      render: (_, inst) =>
        inst.muted ? (
          <Tooltip title={formatMutedUntil(inst.mutedUntil)}>
            <span style={{ color: '#d46b08', fontWeight: 500, fontSize: 12 }}>
              {formatMutedUntil(inst.mutedUntil)}
            </span>
          </Tooltip>
        ) : (
          <span style={{ color: '#bbb', fontSize: 12 }}>{_l('未静默')}</span>
        ),
    },
    {
      title: _l('静默原因'),
      width: 180,
      ellipsis: true,
      render: (_, inst) =>
        inst.muted && inst.muteReason ? (
          <Tooltip title={inst.muteReason}>
            <span style={{ fontSize: 12 }}>{inst.muteReason}</span>
          </Tooltip>
        ) : (
          <span style={{ color: '#bbb' }}>-</span>
        ),
    },
    {
      title: _l('最近更新'),
      width: 155,
      render: (_, inst) => (
        <span style={{ fontFamily: 'monospace', fontSize: 11 }}>{formatTime(inst.updatedAt)}</span>
      ),
    },
    {
      title: _l('操作'),
      width: 80,
      align: 'center',
      render: (_, inst) =>
        inst.muted ? (
          <Button
            size="small"
            type="link"
            onClick={() => onCancelSilence(inst)}
            style={{ color: '#389e0d', padding: 0 }}
          >
            {_l('解除')}
          </Button>
        ) : (
          <Button
            size="small"
            type="link"
            onClick={() => openSilence(inst)}
            style={{ color: '#d46b08', padding: 0 }}
          >
            {_l('静默')}
          </Button>
        ),
    },
  ];

  return (
    <>
      {/* ── Main instances modal ────────────────────────────────────────── */}
      <Modal
        title={title ? `${_l('实例明细与静默')} — ${title}` : _l('实例明细与静默')}
        open={open}
        onCancel={onClose}
        width={1080}
        destroyOnClose
        footer={
          <Space>
            <Button
              icon={<SyncOutlined spin={refreshing} />}
              onClick={onRefresh}
              disabled={loading || refreshing}
            >
              {refreshing ? _l('刷新中...') : _l('刷新明细')}
            </Button>
            <Button type="primary" onClick={onClose}>
              {_l('关闭')}
            </Button>
          </Space>
        }
      >
        {/* Summary row */}
        <Row gutter={12} style={{ marginBottom: 16 }}>
          <Col span={6}>
            <div style={statBoxStyle('#f0f5ff', '#adc6ff')}>
              <div style={{ fontSize: 11, color: '#555' }}>{objectLabel}{_l('总数')}</div>
              <div style={{ fontSize: 22, fontWeight: 700 }}>{total}</div>
            </div>
          </Col>
          <Col span={6}>
            <div style={statBoxStyle('#fff1f0', '#ffa39e')}>
              <div style={{ fontSize: 11, color: '#a8071a' }}>{_l('未静默告警')}</div>
              <div style={{ fontSize: 22, fontWeight: 700, color: '#cf1322' }}>{firingUnmuted}</div>
            </div>
          </Col>
          <Col span={6}>
            <div style={statBoxStyle('#fff7e6', '#ffd591')}>
              <div style={{ fontSize: 11, color: '#ad4e00' }}>{_l('已静默告警')}</div>
              <div style={{ fontSize: 22, fontWeight: 700, color: '#d46b08' }}>{firingMuted}</div>
            </div>
          </Col>
          <Col span={6}>
            <div style={statBoxStyle('#f6ffed', '#b7eb8f')}>
              <div style={{ fontSize: 11, color: '#135200' }}>{_l('正常')}{objectLabel}</div>
              <div style={{ fontSize: 22, fontWeight: 700, color: '#389e0d' }}>{normalCount}</div>
            </div>
          </Col>
        </Row>

        <Table
          loading={loading}
          columns={columns}
          dataSource={instances}
          rowKey="scopeKey"
          size="small"
          pagination={false}
          scroll={{ y: 420, x: '100%' }}
          locale={{ emptyText: emptyText || _l('暂无实例数据') }}
        />
      </Modal>

      {/* ── Silence sub-modal ───────────────────────────────────────────── */}
      <Modal
        title={`${_l('静默')}${objectLabel}`}
        open={!!silenceTarget}
        onCancel={closeSilence}
        width={480}
        destroyOnClose
        footer={
          <Space>
            <Button onClick={closeSilence}>{_l('取消')}</Button>
            <Button type="primary" loading={submitting} onClick={submitSilence}>
              {_l('确认静默')}
            </Button>
          </Space>
        }
      >
        {silenceTarget && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16, marginTop: 8 }}>
            {/* Target info */}
            <div style={{ background: '#fff7e6', border: '1px solid #ffd591', borderRadius: 8, padding: '10px 14px' }}>
              <div style={{ fontSize: 11, color: '#ad4e00', fontWeight: 500, marginBottom: 4 }}>
                {_l('静默对象')}
              </div>
              {(() => {
                const id = formatIdentity(silenceTarget);
                return (
                  <>
                    <div style={{ fontFamily: 'monospace', fontWeight: 600 }}>{id.primary}</div>
                    {id.hostname && (
                      <div style={{ fontSize: 11, color: '#888', marginTop: 2 }}>
                        {_l('主机名')}：{id.hostname}
                      </div>
                    )}
                  </>
                );
              })()}
            </div>

            {/* Duration presets */}
            <div>
              <div style={{ fontSize: 12, fontWeight: 600, color: '#555', marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
                {_l('静默时长')}
              </div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {SILENCE_PRESETS.map((p) => (
                  <Button
                    key={p.value}
                    size="small"
                    type={silencePreset === p.value ? 'primary' : 'default'}
                    danger={p.value === 'permanent' && silencePreset === 'permanent'}
                    onClick={() => { setSilencePreset(p.value); setSilenceUntil(null); }}
                  >
                    {p.label}
                  </Button>
                ))}
              </div>
              {silencePreset === 'custom' && (
                <DatePicker
                  showTime
                  style={{ width: '100%', marginTop: 10 }}
                  value={silenceUntil}
                  onChange={(v) => setSilenceUntil(v)}
                  disabledDate={(d) => d && d.isBefore(dayjs(), 'day')}
                  placeholder={_l('选择截止时间')}
                />
              )}
            </div>

            {/* Reason */}
            <div>
              <div style={{ fontSize: 12, fontWeight: 600, color: '#555', marginBottom: 6 }}>
                {_l('静默原因')}
              </div>
              <TextArea
                rows={3}
                value={silenceReason}
                onChange={(e) => setSilenceReason(e.target.value)}
                placeholder={_l('例如：主机维护、已知故障、迁移窗口...')}
              />
            </div>
          </div>
        )}
      </Modal>
    </>
  );
};

function statBoxStyle(bg, borderColor) {
  return {
    background: bg,
    border: `1px solid ${borderColor}`,
    borderRadius: 8,
    padding: '10px 14px',
  };
}

export default InstancesSilenceModal;
