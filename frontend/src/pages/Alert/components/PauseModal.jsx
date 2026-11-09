/**
 * PauseModal — pause options dialog.
 *
 * Props:
 *   open        {boolean}   controlled visibility
 *   onCancel    {Function}
 *   onConfirm   {Function}  (payload: { mode, pausedUntil?, reason }) => Promise<void>|void
 *   count       {number}    number of monitors being paused (shown in info text)
 *   loading     {boolean}
 *   title       {string}    modal title, defaults to '暂停监控'
 */
import React, { useEffect,useState } from 'react';
import { Alert, Button,DatePicker, Input, InputNumber, Modal, Radio, Space } from 'antd';
import dayjs from 'dayjs';

import { _l } from '@/utils/utils';

const { TextArea } = Input;

const QUICK_HOURS = [
  { label: _l('30 分钟'), value: 0.5 },
  { label: _l('1 小时'),  value: 1 },
  { label: _l('2 小时'),  value: 2 },
  { label: _l('5 小时'),  value: 5 },
  { label: _l('1 天'),    value: 24 },
];

const PauseModal = ({
  open,
  onCancel,
  onConfirm,
  count = 1,
  loading = false,
  title,
}) => {
  const [kind, setKind]               = useState('manual');
  const [durationHours, setDuration]  = useState(5);
  const [until, setUntil]             = useState(null);
  const [reason, setReason]           = useState('');
  const [err, setErr]                 = useState('');

  useEffect(() => {
    if (open) {
      setKind('manual');
      setDuration(5);
      setUntil(null);
      setReason('');
      setErr('');
    }
  }, [open]);

  const getPreview = () => {
    if (kind === 'manual') return _l('需要手动启用后才会继续监控');
    if (kind === 'duration') {
      const t = dayjs().add(durationHours * 60, 'minute');
      return `${_l('将于')} ${t.format('YYYY-MM-DD HH:mm')} ${_l('自动启用')}`;
    }
    if (!until) return _l('请选择有效时间');
    return `${_l('将于')} ${until.format('YYYY-MM-DD HH:mm')} ${_l('自动启用')}`;
  };

  const handleOk = async () => {
    setErr('');
    const payload = { mode: kind === 'manual' ? 'manual' : 'temporary', reason: reason.trim() };
    if (kind === 'duration') {
      if (!durationHours || durationHours <= 0) { setErr(_l('暂停时长必须大于 0')); return; }
      payload.pausedUntil = dayjs().add(durationHours * 60, 'minute').toISOString();
    }
    if (kind === 'until') {
      if (!until || until.isBefore(dayjs())) { setErr(_l('指定时间必须晚于当前时间')); return; }
      payload.pausedUntil = until.toISOString();
    }
    await onConfirm(payload);
  };

  return (
    <Modal
      title={title || _l('暂停监控')}
      open={open}
      onCancel={onCancel}
      width={520}
      destroyOnClose
      footer={
        <Space>
          <Button onClick={onCancel}>{_l('取消')}</Button>
          <Button type="primary" danger loading={loading} onClick={handleOk}>
            {_l('确认暂停')}
          </Button>
        </Space>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16, marginTop: 8 }}>
        <Alert
          type="warning"
          showIcon={false}
          message={`${_l('已选择 %0 条监控，暂停期间不会执行周期检查，也不会发送告警通知。', count)}`}
        />

        <div>
          <div style={{ fontWeight: 500, marginBottom: 8, fontSize: 13 }}>{_l('暂停方式')}</div>
          <Radio.Group value={kind} onChange={(e) => { setKind(e.target.value); setErr(''); }}>
            <Space direction="vertical">
              <Radio value="manual">{_l('永久暂停（需手动恢复）')}</Radio>
              <Radio value="duration">{_l('暂停时长')}</Radio>
              <Radio value="until">{_l('指定截止时间')}</Radio>
            </Space>
          </Radio.Group>
        </div>

        {kind === 'duration' && (
          <div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
              {QUICK_HOURS.map((q) => (
                <Button
                  key={q.value}
                  size="small"
                  type={durationHours === q.value ? 'primary' : 'default'}
                  onClick={() => setDuration(q.value)}
                >
                  {q.label}
                </Button>
              ))}
            </div>
            <InputNumber
              style={{ width: '100%' }}
              min={0.1}
              step={0.5}
              value={durationHours}
              onChange={(v) => setDuration(v)}
              addonAfter={_l('小时')}
              placeholder="5"
            />
          </div>
        )}

        {kind === 'until' && (
          <DatePicker
            showTime
            style={{ width: '100%' }}
            value={until}
            onChange={(v) => { setUntil(v); setErr(''); }}
            disabledDate={(d) => d && d.isBefore(dayjs(), 'day')}
            placeholder={_l('选择截止时间')}
          />
        )}

        <div>
          <div style={{ fontWeight: 500, marginBottom: 6, fontSize: 13 }}>
            {_l('暂停原因（可选）')}
          </div>
          <TextArea
            rows={3}
            maxLength={500}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={_l('例如：版本发布、数据库维护、网络割接')}
          />
        </div>

        <div style={{ padding: '10px 12px', background: '#f9fafb', borderRadius: 8, fontSize: 13, color: '#555' }}>
          {getPreview()}
        </div>

        {err && <div style={{ color: '#f5222d', fontSize: 13 }}>{err}</div>}
      </div>
    </Modal>
  );
};

export default PauseModal;
