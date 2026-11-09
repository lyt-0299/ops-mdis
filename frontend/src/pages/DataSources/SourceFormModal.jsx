/**
 * SourceFormModal — 数据源新建/编辑右侧 Modal 表单
 *
 * Props:
 *   open            {boolean}     controlled visibility
 *   type            {string}      host / mysql / mongodb / kafka / redis / elasticsearch
 *   record          {Object|null} null = 新建；object = 编辑（带已有字段值）
 *   onClose         {Function}    关闭回调
 *   onSaved         {Function}    保存成功后回调（触发父刷新）
 *   existingSources {Array}       父层现有数据源列表，用于保存前重复地址软提示
 */
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { MinusCircleOutlined, PlusOutlined, SyncOutlined } from '@ant-design/icons';
import {
  Alert,
  Button,
  Checkbox,
  Divider,
  Form,
  Input,
  InputNumber,
  message,
  Modal,
  Select,
  Space,
  Switch,
  Tag,
  Tooltip,
} from 'antd';

import { SourceTypeTitle } from '@/components/SourceTypeLogo';
import {
  checkSourceDuplicate,
  createEsInstance,
  createHostInstance,
  createKafkaInstance,
  createMongoInstance,
  createMysqlInstance,
  createRedisInstance,
  probeDataSource,
  testDataSource,
  updateEsInstance,
  updateHostInstance,
  updateKafkaInstance,
  updateMongoInstance,
  updateMysqlInstance,
  updateRedisInstance,
} from '@/utils/alertApi';
import { apiErr, keyText } from '@/utils/apiError';
import { _l } from '@/utils/utils';

const { TextArea } = Input;
const { Option } = Select;

// ── Constants ─────────────────────────────────────────────────────────────────

const PURPOSE_OPTIONS_FULL = [
  { value: 'metrics', label: _l('看图') },
  { value: 'alert', label: _l('告警') },
  { value: 'diagnose', label: _l('慢查询诊断') },
];

const PURPOSE_OPTIONS_SHORT = [
  { value: 'metrics', label: _l('看图') },
  { value: 'alert', label: _l('告警') },
];

const DEFAULT_PURPOSES = ['metrics', 'alert'];

// 每种类型支持的用途列表（探测+三态显示范围）
const TYPE_PURPOSES = {
  mongodb:       ['metrics', 'alert', 'diagnose'],
  mysql:         ['metrics', 'alert'],
  kafka:         ['metrics', 'alert'],
  redis:         ['metrics'],
  elasticsearch: ['metrics'],
  host:          ['metrics'],
};

// 用途显示名（与 PURPOSE_OPTIONS 保持同步，但不使用 Option 组件）
const PURPOSE_LABEL = {
  metrics:  _l('看图'),
  alert:    _l('告警'),
  diagnose: _l('慢查询诊断'),
};

const SASL_MECHANISMS = [
  { value: 'PLAIN', label: 'PLAIN' },
  { value: 'SCRAM-SHA-256', label: 'SCRAM-SHA-256' },
  { value: 'SCRAM-SHA-512', label: 'SCRAM-SHA-512' },
];

const ARCHITECTURE_OPTIONS = [
  { value: 'standalone', label: _l('单机') },
  { value: 'replication', label: _l('主从复制') },
  { value: 'mgr', label: _l('MGR（组复制）') },
];

const ADDRESS_PATTERN = /^[^\s:]+:\d{1,5}$/;

// ── PurposeSection: 三态用途开关区 ───────────────────────────────────────────
//
// Props:
//   type         {string}   当前数据源类型
//   probing      {boolean}  探测进行中
//   probeResult  {Object|null}  探测结果 { connect: {ok, message}, purposes: {...} }
//   checked      {Object}   { [purpose]: boolean }
//   onChange     {Function} (purpose, value) => void
//
const PurposeSection = ({ type, probing, probeResult, checked, onChange }) => {
  const purposes = TYPE_PURPOSES[type] || [];
  if (!purposes.length) return null;

  const connectFailed = probeResult && !probeResult.connect?.ok;

  return (
    <Form.Item label={_l('用途')}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {connectFailed && (
          <div style={{ marginBottom: 4, fontSize: 12, color: 'var(--color-error, #ff4d4f)' }}>
            {probeResult.connect.message || _l('连接失败，用途探测不可用')}
          </div>
        )}
        {purposes.map((p) => {
          const cap = probeResult?.purposes?.[p];
          // 三态：probing → 检测中；connect失败 → 全灰；有结果 → 按 available
          let statusTag;
          let switchDisabled;
          if (probing) {
            statusTag = <Tag>{_l('检测中…')}</Tag>;
            switchDisabled = true;
          } else if (!probeResult) {
            // 未探测
            statusTag = <Tag>{_l('未检测')}</Tag>;
            switchDisabled = false;
          } else if (connectFailed) {
            statusTag = <Tag>{_l('不可用')}</Tag>;
            switchDisabled = true;
          } else if (cap?.available) {
            statusTag = <Tag color="success">{_l('可用')}</Tag>;
            switchDisabled = false;
          } else if (cap && !cap.available) {
            // reasonKey 是后端给的英文机器键（文案归前端），reason 为尚未 key 化的兜底
            const reasonText = (cap.reasonKey && keyText(cap.reasonKey)) || cap.reason || _l('不支持');
            statusTag = (
              <Tooltip title={reasonText}>
                <Tag>{_l('不可用')}</Tag>
              </Tooltip>
            );
            switchDisabled = true;
          } else {
            // probeResult 存在但该 purpose 不在返回结果中 → 视作不可用，禁止开启
            statusTag = <Tag>{_l('未知')}</Tag>;
            switchDisabled = true;
          }

          return (
            <div key={p} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={{ width: 80, flexShrink: 0 }}>{PURPOSE_LABEL[p] || p}</span>
              {statusTag}
              <Switch
                size="small"
                disabled={switchDisabled}
                checked={!!checked?.[p]}
                onChange={(v) => onChange(p, v)}
              />
              {/* 原因说明（不可用时红字） */}
              {!probing && probeResult && cap && !cap.available && (cap.reasonKey || cap.reason) && (
                <span style={{ fontSize: 11, color: 'var(--color-error, #ff4d4f)' }}>
                  {(cap.reasonKey && keyText(cap.reasonKey)) || cap.reason}
                </span>
              )}
            </div>
          );
        })}
      </div>
    </Form.Item>
  );
};

// ── Helpers ───────────────────────────────────────────────────────────────────

const brokersToText = (brokers) =>
  Array.isArray(brokers) ? brokers.join('\n') : (brokers || '');

const textToBrokers = (text) =>
  (text || '').split(/[\n,]+/).map((s) => s.trim()).filter(Boolean);

const nodesToText = (nodes) =>
  Array.isArray(nodes) ? nodes.join('\n') : (nodes || '');

const textToNodes = (text) =>
  (text || '').split(/[\n,]+/).map((s) => s.trim()).filter(Boolean);

// 首节点回填 host/port：Redis 的连接测试、告警引擎仍读这两个旧字段，
// 只写 nodes 会让它们拿到空地址。
const splitHostPort = (node, defPort) => {
  const i = (node || '').lastIndexOf(':');
  if (i > 0) {
    const p = Number(node.slice(i + 1));
    if (Number.isInteger(p) && p > 0) return [node.slice(0, i), p];
  }
  return [node || '', defPort];
};

// 按 type 获取空表单初始值
const getEmptyValues = (type) => {
  switch (type) {
    case 'host':
      return { name: '', address: '' };
    case 'kafka':
      return { name: '', brokersText: '', saslEnable: false, saslMechanism: 'PLAIN', saslUsername: '', saslPassword: '', purposes: DEFAULT_PURPOSES };
    case 'mongodb':
      return { name: '', uri: '', username: '', password: '', authDb: '', tls: false, skipSSL: false, purposes: DEFAULT_PURPOSES };
    case 'mysql':
      return { name: '', architecture: 'standalone', nodesText: '', username: '', password: '', database: 'mysql', tls: false, skipSSL: false, purposes: DEFAULT_PURPOSES };
    case 'redis':
      return { name: '', nodesText: '', password: '', db: 0, tls: false, skipSSL: false };
    case 'elasticsearch':
      return { name: '', nodesText: '', username: '', password: '', skipSSL: false };
    default:
      return {};
  }
};

// 从 record 还原表单值（编辑）
const getEditValues = (type, record) => {
  if (!record) return getEmptyValues(type);
  switch (type) {
    case 'host':
      return {
        name: record.name || '',
        address: record.detail?.address || record.address || '',
      };
    case 'kafka':
      return {
        name: record.name || '',
        brokersText: brokersToText(record.detail?.brokers || record.brokers),
        saslEnable: record.detail?.sasl?.enabled || record.sasl?.enabled || false,
        saslMechanism: record.detail?.sasl?.mechanism || record.sasl?.mechanism || 'PLAIN',
        saslUsername: record.detail?.sasl?.username || record.sasl?.username || '',
        saslPassword: '',
        purposes: Array.isArray(record.purposes) && record.purposes.length ? record.purposes : DEFAULT_PURPOSES,
      };
    case 'mongodb':
      return {
        name: record.name || '',
        uri: record.detail?.uri || record.uri || '',
        username: record.detail?.username || record.username || '',
        password: '',
        collectorSelections: (record.detail?.metricCollection || record.metricCollection)?.collectors || [],
        collectorScope: ((record.detail?.metricCollection || record.metricCollection)?.scope || []).join('\n'),
        authDb: record.detail?.authDb || record.authDb || '',
        tls: record.detail?.tls || record.tls || false,
        skipSSL: record.detail?.skipSSL || record.skipSSL || false,
        purposes: Array.isArray(record.purposes) && record.purposes.length ? record.purposes : DEFAULT_PURPOSES,
      };
    case 'mysql':
      return {
        name: record.name || '',
        architecture: record.detail?.architecture || record.architecture || 'standalone',
        nodesText: nodesToText(record.detail?.nodes || record.nodes),
        username: record.detail?.username || record.username || '',
        password: '',
        collectorSelections: (record.detail?.metricCollection || record.metricCollection)?.collectors || [],
        collectorScope: ((record.detail?.metricCollection || record.metricCollection)?.scope || []).join('\n'),
        database: record.detail?.database || record.database || 'mysql',
        tls: record.detail?.tls || record.tls || false,
        skipSSL: record.detail?.skipSSL || record.skipSSL || false,
        purposes: Array.isArray(record.purposes) && record.purposes.length ? record.purposes : DEFAULT_PURPOSES,
      };
    case 'redis':
      return {
        // 旧数据只有 host/port，拼成一行填进多节点框
        nodesText: nodesToText(
          (record.detail?.nodes || record.nodes)
          || [`${record.detail?.host || record.host || ''}:${record.detail?.port || record.port || 6379}`].filter((x) => x !== ':6379'),
        ),
        name: record.name || '',
        host: record.detail?.host || record.host || '',
        port: record.detail?.port || record.port || 6379,
        password: '',
        db: record.detail?.db || record.db || 0,
        tls: record.detail?.tls || record.tls || false,
        skipSSL: record.detail?.skipSSL || record.skipSSL || false,
      };
    case 'elasticsearch':
      return {
        name: record.name || '',
        url: record.detail?.url || record.url || '',
        username: record.detail?.username || record.username || '',
        password: '',
        skipSSL: record.detail?.skipSSL || record.skipSSL || false,
      };
    default:
      return {};
  }
};

// 组装保存 payload
const buildPayload = (type, values, isEdit) => {
  switch (type) {
    case 'host':
      return {
        name: values.name.trim(),
        address: values.address.trim(),
        purposes: ['metrics'],
      };
    case 'kafka': {
      const brokers = textToBrokers(values.brokersText);
      const payload = {
        name: values.name.trim(),
        brokers,
        purposes: Array.isArray(values.purposes) ? values.purposes : DEFAULT_PURPOSES,
        sasl: values.saslEnable
          ? { enabled: true, mechanism: values.saslMechanism, username: values.saslUsername, ...(values.saslPassword ? { password: values.saslPassword } : {}) }
          : { enabled: false },
      };
      return payload;
    }
    case 'mongodb': {
      const payload = {
        name: values.name.trim(),
        uri: values.uri.trim(),
        username: values.username?.trim() || '',
        authDb: values.authDb?.trim() || '',
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
        metricCollection: {
          collectors: values.collectorSelections || [],
          scope: textToNodes(values.collectorScope),
        },
        purposes: Array.isArray(values.purposes) ? values.purposes : DEFAULT_PURPOSES,
      };
      if (values.password) payload.password = values.password;
      return payload;
    }
    case 'mysql': {
      const nodes = textToNodes(values.nodesText);
      const payload = {
        name: values.name.trim(),
        architecture: values.architecture || 'standalone',
        nodes,
        username: values.username?.trim() || '',
        database: values.database?.trim() || 'mysql',
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
        metricCollection: {
          collectors: values.collectorSelections || [],
          scope: textToNodes(values.collectorScope),
        },
        purposes: Array.isArray(values.purposes) ? values.purposes : DEFAULT_PURPOSES,
      };
      if (values.password) payload.password = values.password;
      return payload;
    }
    case 'redis': {
      const nodes = textToNodes(values.nodesText);
      const [firstHost, firstPort] = splitHostPort(nodes[0] || '', 6379);
      const payload = {
        name: values.name.trim(),
        nodes,
        host: firstHost,
        port: firstPort,
        db: values.db || 0,
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
        purposes: ['metrics'],
      };
      if (values.password) payload.password = values.password;
      return payload;
    }
    case 'elasticsearch': {
      const nodes = textToNodes(values.nodesText);
      const payload = {
        name: values.name.trim(),
        nodes,
        url: nodes[0] || '',
        username: values.username?.trim() || '',
        skipSSL: values.skipSSL || false,
        purposes: ['metrics'],
      };
      if (values.password) payload.password = values.password;
      return payload;
    }
    default:
      return {};
  }
};

// 从表单值或存储的 detail 中提取用于查重的地址摘要（提取失败返回 null）
// values 可以是表单值（含 brokersText/nodesText 文本字段）或存储的 detail（含 brokers/nodes 数组）
const extractAddrSummary = (type, values) => {
  try {
    switch (type) {
      case 'mongodb': {
        // 提取 URI 中的 host 段：mongodb://host:port/db → host:port
        const uri = (values.uri || '').trim();
        const m = uri.match(/^mongodb(?:\+srv)?:\/\/(?:[^@]+@)?([^/?]+)/i);
        return m ? m[1] : null;
      }
      case 'redis': {
        const nodes = Array.isArray(values.nodes) ? values.nodes : textToNodes(values.nodesText || '');
        if (nodes.length) return [...nodes].sort().join(',');
        return values.host ? `${(values.host || '').trim()}:${values.port || 6379}` : null;
      }
      case 'kafka': {
        // 表单侧: brokersText (string)；存储侧: brokers (array)
        let brokers;
        if (Array.isArray(values.brokers)) {
          brokers = values.brokers;
        } else {
          brokers = textToBrokers(values.brokersText || '');
        }
        return brokers.length ? [...brokers].sort().join(',') : null;
      }
      case 'mysql': {
        // 表单侧: nodesText (string)；存储侧: nodes (array)
        let nodes;
        if (Array.isArray(values.nodes)) {
          nodes = values.nodes;
        } else {
          nodes = textToNodes(values.nodesText || '');
        }
        return nodes.length ? [...nodes].sort().join(',') : null;
      }
      case 'elasticsearch': {
        const nodes = Array.isArray(values.nodes) ? values.nodes : textToNodes(values.nodesText || '');
        if (nodes.length) return [...nodes].sort().join(',');
        return (values.url || '').trim() || null;
      }
      case 'host':
        return (values.address || '').trim() || null;
      default:
        return null;
    }
  } catch {
    return null;
  }
};

// 调对应 create/update API
const callSaveApi = (type, id, payload) => {
  switch (type) {
    case 'host':
      return id ? updateHostInstance(id, payload) : createHostInstance(payload);
    case 'kafka':
      return id ? updateKafkaInstance(id, payload) : createKafkaInstance(payload);
    case 'mongodb':
      return id ? updateMongoInstance(id, payload) : createMongoInstance(payload);
    case 'mysql':
      return id ? updateMysqlInstance(id, payload) : createMysqlInstance(payload);
    case 'redis':
      return id ? updateRedisInstance(id, payload) : createRedisInstance(payload);
    case 'elasticsearch':
      return id ? updateEsInstance(id, payload) : createEsInstance(payload);
    default:
      return Promise.reject(new Error('Unknown type'));
  }
};

// Modal 标题
const modalTitle = (type, isEdit) => {
  const actionLabel = isEdit ? _l('编辑') : _l('新建');
  const typeLabel = {
    host: _l('主机'),
    kafka: 'Kafka',
    mongodb: 'MongoDB',
    mysql: 'MySQL',
    redis: 'Redis',
    elasticsearch: 'Elasticsearch',
  }[type] || type;
  return `${actionLabel} ${typeLabel}`;
};

// ── Form field blocks per type ────────────────────────────────────────────────

// 密钥输入框三态提示：新建保持原样；编辑+已配置/未配置给出明确信号
const secretFieldHints = (editing, hasSecret, newPlaceholder = '', newExtra = '') => {
  if (!editing) return { placeholder: newPlaceholder, extra: newExtra };
  return hasSecret
    ? { placeholder: _l('已配置（保密，不回显）'), extra: _l('已配置，留空表示不修改') }
    : { placeholder: _l('未配置'), extra: _l('留空表示不修改') };
};

const HostFields = () => (
  <>
    <Form.Item
      name="name"
      label={_l('实例名称')}
      rules={[{ required: true, message: _l('请输入实例名称') }]}
    >
      <Input placeholder={_l('例如：生产环境 50 主机')} />
    </Form.Item>

    <Form.Item
      name="address"
      label={_l('node_exporter 地址')}
      rules={[
        { required: true, message: _l('请输入 node_exporter 地址') },
        { pattern: ADDRESS_PATTERN, message: _l('格式应为 host:port，例如 172.17.30.50:59100') },
      ]}
      extra={_l('node_exporter 暴露的地址，格式 host:port')}
    >
      <Input
        placeholder="172.17.30.50:59100"
        style={{ fontFamily: 'monospace', fontSize: 12 }}
      />
    </Form.Item>
  </>
);

const KafkaFields = ({ editing, hasSecret, form, purposeSection }) => {
  const saslEnable = Form.useWatch('saslEnable', form);
  const saslPwdHints = secretFieldHints(editing, hasSecret);
  return (
    <>
      <Form.Item
        name="name"
        label={_l('实例名称')}
        rules={[{ required: true, message: _l('请输入实例名称') }]}
      >
        <Input placeholder={_l('例如：生产环境 Kafka')} />
      </Form.Item>

      {purposeSection}

      <Form.Item
        name="brokersText"
        label={_l('Broker 地址')}
        rules={[{ required: true, message: _l('请输入 Broker 地址') }]}
        extra={_l('每行或以逗号分隔一个 Broker，例如 kafka1:9092')}
      >
        <TextArea
          rows={3}
          placeholder={'kafka1:9092\nkafka2:9092\nkafka3:9092'}
          style={{ fontFamily: 'monospace', fontSize: 12 }}
        />
      </Form.Item>

      <Divider style={{ margin: '12px 0' }}>{_l('SASL 认证（可选）')}</Divider>

      <Form.Item name="saslEnable" label={_l('启用 SASL')} valuePropName="checked">
        <Switch />
      </Form.Item>

      {saslEnable && (
        <>
          <Form.Item name="saslMechanism" label={_l('SASL 机制')}>
            <Select>
              {SASL_MECHANISMS.map((m) => (
                <Option key={m.value} value={m.value}>{m.label}</Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="saslUsername"
            label={_l('用户名')}
            rules={[{ required: true, message: _l('请输入用户名') }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="saslPassword"
            label={_l('密码')}
            extra={saslPwdHints.extra}
            rules={editing ? [] : [{ required: true, message: _l('请输入密码') }]}
          >
            <Input.Password autoComplete="new-password" placeholder={saslPwdHints.placeholder} />
          </Form.Item>
        </>
      )}
    </>
  );
};

const MetricCollectionFields = ({ form, type }) => {
  const selections = Form.useWatch('collectorSelections', form) || [];
  const mongo = type === 'mongodb';
  const needsScope = selections.some((item) => ['collections', 'indexes', 'tables'].includes(item));
  const options = mongo ? [
    { value: 'databases', label: _l('库容量') },
    { value: 'collections', label: _l('集合容量') },
    { value: 'indexes', label: _l('索引访问') },
    { value: 'operations', label: _l('集合读写活动') },
  ] : [
    { value: 'tables', label: _l('表容量') },
    { value: 'tableIO', label: _l('表级 I/O 等待') },
    { value: 'indexIO', label: _l('索引 I/O 等待') },
    { value: 'tableLocks', label: _l('表锁等待') },
    { value: 'statements', label: _l('SQL 模板统计') },
    { value: 'innodb', label: _l('InnoDB 扩展指标') },
  ];
  const scopeLabel = mongo ? _l('集合范围') : _l('数据库范围');
  return (
    <>
      <Form.Item
        name="collectorSelections"
        label={_l('扩展指标采集')}
        extra={mongo ? _l('启用「看图」后生效，采集账号需要读取所选统计的权限。')
          : _l('启用「看图」后生效，I/O、锁和 SQL 统计依赖 Performance Schema。')}
      >
        <Checkbox.Group options={options} />
      </Form.Item>
      {needsScope && (
        <Form.Item
          name="collectorScope"
          label={scopeLabel}
          preserve={false}
          rules={[{ required: true, whitespace: true }]}
          extra={mongo ? _l('每行填写一个库名.集合名，也可用逗号分隔。填写 * 可发现所有集合。')
            : _l('每行填写一个库名，也可用逗号分隔。填写 * 可采集所有库的表容量。')}
        >
          <Input.TextArea rows={2} placeholder={mongo ? 'mdwsrows.ws_collection' : 'app_db'} />
        </Form.Item>
      )}
    </>
  );
};

const MongoFields = ({ editing, hasSecret, form, purposeSection }) => {
  const tlsEnabled = Form.useWatch('tls', form);
  const pwdHints = secretFieldHints(editing, hasSecret, _l('密码'));
  return (
    <>
      <Form.Item
        name="name"
        label={_l('实例名称')}
        rules={[{ required: true, message: _l('请输入实例名称') }]}
      >
        <Input placeholder={_l('例如：生产环境 MongoDB 副本集')} />
      </Form.Item>

      {purposeSection}
      <MetricCollectionFields form={form} type="mongodb" />

      <Form.Item
        name="uri"
        label="MongoDB URI"
        rules={[{ required: true, message: _l('请输入 MongoDB 连接地址') }]}
        extra={_l('格式：mongodb://host:port 或 mongodb+srv://...')}
      >
        <Input
          placeholder="mongodb://host:27017"
          style={{ fontFamily: 'monospace', fontSize: 12 }}
        />
      </Form.Item>

      <Divider style={{ margin: '12px 0' }}>{_l('认证（可选）')}</Divider>

      <Form.Item label={_l('用户名 / 密码')} style={{ marginBottom: 0 }} extra={pwdHints.extra}>
        <Space style={{ display: 'flex' }}>
          <Form.Item name="username" noStyle>
            <Input placeholder={_l('用户名')} autoComplete="off" style={{ width: 200 }} />
          </Form.Item>
          <Form.Item name="password" noStyle>
            <Input.Password
              placeholder={pwdHints.placeholder}
              autoComplete="new-password"
              style={{ width: 200 }}
            />
          </Form.Item>
        </Space>
      </Form.Item>

      <Form.Item
        name="authDb"
        label={_l('认证数据库')}
        style={{ marginTop: 16 }}
        extra={_l('留空默认使用 admin')}
      >
        <Input placeholder="admin" style={{ width: 200 }} />
      </Form.Item>

      <Divider style={{ margin: '12px 0' }}>TLS</Divider>

      <Form.Item name="tls" label={_l('启用 TLS')} valuePropName="checked">
        <Switch />
      </Form.Item>

      {tlsEnabled && (
        <Form.Item name="skipSSL" label={_l('跳过证书验证 (InsecureSkipVerify)')} valuePropName="checked">
          <Switch />
        </Form.Item>
      )}
    </>
  );
};

const MySQLFields = ({ editing, hasSecret, form, purposeSection }) => {
  const tlsEnabled = Form.useWatch('tls', form);
  const pwdHints = secretFieldHints(editing, hasSecret, _l('密码'));
  return (
    <>
      <Form.Item
        name="name"
        label={_l('实例名称')}
        rules={[{ required: true, message: _l('请输入实例名称') }]}
      >
        <Input placeholder={_l('例如：生产环境 MySQL 主从')} />
      </Form.Item>

      {purposeSection}
      <MetricCollectionFields form={form} type="mysql" />

      <Form.Item
        name="architecture"
        label={_l('集群架构')}
        rules={[{ required: true, message: _l('请选择架构') }]}
      >
        <Select>
          {ARCHITECTURE_OPTIONS.map((o) => (
            <Option key={o.value} value={o.value}>{o.label}</Option>
          ))}
        </Select>
      </Form.Item>

      <Form.Item
        name="nodesText"
        label={_l('节点地址')}
        rules={[{ required: true, message: _l('请输入节点地址') }]}
        extra={_l('每行或以逗号分隔一个节点，格式 host:port，例如 db1:3306')}
      >
        <TextArea
          rows={3}
          placeholder={'db1:3306\ndb2:3306\ndb3:3306'}
          style={{ fontFamily: 'monospace', fontSize: 12 }}
        />
      </Form.Item>

      <Divider style={{ margin: '12px 0' }}>{_l('认证')}</Divider>

      <Form.Item label={_l('用户名 / 密码')} style={{ marginBottom: 0 }} extra={pwdHints.extra}>
        <Space style={{ display: 'flex' }}>
          <Form.Item name="username" noStyle>
            <Input placeholder={_l('用户名')} autoComplete="off" style={{ width: 200 }} />
          </Form.Item>
          <Form.Item name="password" noStyle>
            <Input.Password
              placeholder={pwdHints.placeholder}
              autoComplete="new-password"
              style={{ width: 200 }}
            />
          </Form.Item>
        </Space>
      </Form.Item>

      <Form.Item
        name="database"
        label={_l('连接数据库')}
        style={{ marginTop: 16 }}
        extra={_l('用于连接和检查状态的数据库名，默认 mysql')}
      >
        <Input placeholder="mysql" style={{ width: 200 }} />
      </Form.Item>

      <Divider style={{ margin: '12px 0' }}>TLS</Divider>

      <Form.Item name="tls" label={_l('启用 TLS')} valuePropName="checked">
        <Switch />
      </Form.Item>

      {tlsEnabled && (
        <Form.Item name="skipSSL" label={_l('跳过证书验证 (InsecureSkipVerify)')} valuePropName="checked">
          <Switch />
        </Form.Item>
      )}
    </>
  );
};

const RedisFields = ({ editing, hasSecret, form }) => {
  const tlsEnabled = Form.useWatch('tls', form);
  const pwdHints = secretFieldHints(editing, hasSecret, _l('密码（可选）'));
  return (
    <>
      <Form.Item
        name="name"
        label={_l('实例名称')}
        rules={[{ required: true, message: _l('请输入实例名称') }]}
      >
        <Input placeholder={_l('例如：生产环境 Redis')} />
      </Form.Item>

      <Form.Item
        name="nodesText"
        label={_l('节点地址')}
        rules={[{ required: true, message: _l('请输入节点地址') }]}
        extra={_l('每行或以逗号分隔一个节点，格式 host:port，例如 redis1:6379。哨兵/集群部署把各节点都填上，每个节点单独采集，主从切换不用改配置')}
      >
        <Input.TextArea
          rows={3}
          placeholder={'redis1:6379\nredis2:6379\nredis3:6379'}
          style={{ fontFamily: 'monospace', fontSize: 12 }}
        />
      </Form.Item>

      <Form.Item
        name="password"
        label={_l('密码')}
        style={{ marginTop: 16 }}
        extra={pwdHints.extra}
      >
        <Input.Password
          placeholder={pwdHints.placeholder}
          autoComplete="new-password"
        />
      </Form.Item>

      <Form.Item name="db" label={_l('数据库编号 (DB)')}>
        <InputNumber min={0} max={255} style={{ width: 160 }} />
      </Form.Item>

      <Divider style={{ margin: '12px 0' }}>TLS</Divider>

      <Form.Item name="tls" label={_l('启用 TLS')} valuePropName="checked">
        <Switch />
      </Form.Item>

      {tlsEnabled && (
        <Form.Item name="skipSSL" label={_l('跳过证书验证 (InsecureSkipVerify)')} valuePropName="checked">
          <Switch />
        </Form.Item>
      )}
    </>
  );
};

const EsFields = ({ editing, hasSecret }) => {
  const pwdHints = secretFieldHints(editing, hasSecret, _l('密码'));
  return (
  <>
    <Form.Item
      name="name"
      label={_l('实例名称')}
      rules={[{ required: true, message: _l('请输入实例名称') }]}
    >
      <Input placeholder={_l('例如：生产环境 Elasticsearch')} />
    </Form.Item>

    <Form.Item
      name="nodesText"
      label="URL"
      rules={[
        { required: true, message: _l('请输入 URL') },
        {
          validator: (_r, v) => {
            const bad = textToNodes(v).filter((u) => !/^https?:\/\/[\w.-]+(:\d+)?(\/.*)?$/i.test(u));
            return bad.length
              ? Promise.reject(new Error(_l('地址格式不对：%0', bad[0])))
              : Promise.resolve();
          },
        },
      ]}
      extra={_l('每行或以逗号分隔一个节点，格式 http(s)://host:9200。填多个时每个节点单独采集，任一节点挂掉不至于整条源断采')}
    >
      <Input.TextArea
        rows={3}
        placeholder={'http://es1:9200\nhttp://es2:9200'}
        style={{ fontFamily: 'monospace', fontSize: 12 }}
      />
    </Form.Item>

    <Divider style={{ margin: '12px 0' }}>{_l('认证（可选）')}</Divider>

    <Form.Item label={_l('用户名 / 密码')} style={{ marginBottom: 0 }} extra={pwdHints.extra}>
      <Space style={{ display: 'flex' }}>
        <Form.Item name="username" noStyle>
          <Input placeholder={_l('用户名')} autoComplete="off" style={{ width: 200 }} />
        </Form.Item>
        <Form.Item name="password" noStyle>
          <Input.Password
            placeholder={pwdHints.placeholder}
            autoComplete="new-password"
            style={{ width: 200 }}
          />
        </Form.Item>
      </Space>
    </Form.Item>

    <Divider style={{ margin: '12px 0' }}>TLS</Divider>

    <Form.Item name="skipSSL" label={_l('跳过 SSL 证书验证')} valuePropName="checked">
      <Switch />
    </Form.Item>
  </>
  );
};


const buildTestPayload = (type, values, editingId) => {
  switch (type) {
    case 'kafka': {
      const brokers = textToBrokers(values.brokersText);
      return {
        type,
        id: editingId,
        brokers,
        sasl: values.saslEnable
          ? { enabled: true, mechanism: values.saslMechanism, username: values.saslUsername, password: values.saslPassword || '' }
          : { enabled: false },
      };
    }
    case 'mongodb':
      return {
        type,
        id: editingId,
        uri: (values.uri || '').trim(),
        username: values.username?.trim() || '',
        password: values.password || '',
        authDb: values.authDb?.trim() || '',
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
      };
    case 'mysql': {
      const nodes = textToNodes(values.nodesText);
      return {
        type,
        id: editingId,
        nodes,
        username: values.username?.trim() || '',
        password: values.password || '',
        database: values.database?.trim() || '',
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
      };
    }
    case 'redis': {
      // 多节点时用首节点做连通性探测：探测只回答「地址和凭据对不对」，
      // 逐个节点探属于采集侧的事，由「采集状态」列反映。
      const [h, p] = splitHostPort(textToNodes(values.nodesText)[0] || '', 6379);
      return {
        type,
        id: editingId,
        host: h,
        port: p,
        password: values.password || '',
        db: values.db || 0,
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
      };
    }
    case 'elasticsearch':
      return {
        type,
        id: editingId,
        url: textToNodes(values.nodesText)[0] || '',
        username: values.username?.trim() || '',
        password: values.password || '',
        skipSSL: values.skipSSL || false,
      };
    case 'host':
      // 此前落到 default 分支,payload 里根本没有 address ——
      // 后端 resolveHostAddress 拿不到地址,新建时必然探不了(编辑时才能靠 id 回填)
      return {
        type,
        id: editingId,
        address: (values.address || '').trim(),
      };
    default:
      return { type, id: editingId };
  }
};

// host 在列表页可测;Modal 内 host 由自动探测覆盖,不单列测试按钮
const TYPE_HAS_TEST = ['kafka', 'mongodb', 'mysql', 'redis', 'elasticsearch'];

// 会走自动探测的类型。host 此前不在其中——填错地址零反馈,
// 只能等 Grafana 面板空了才回头怀疑,这正是「要用户自己闭环」最该补的两类。
const TYPE_HAS_PROBE = [...TYPE_HAS_TEST, 'host'];

// ── Main Component ────────────────────────────────────────────────────────────

const SourceFormModal = ({ open, type, record, onClose, onSaved, existingSources, onEditOther }) => {
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null);

  // ── 探测状态 ────────────────────────────────────────────────────────────────
  const [probing, setProbing] = useState(false);
  // probeResult: null | { connect: { ok, message }, purposes: { [purpose]: { available, reason } } }
  const [probeResult, setProbeResult] = useState(null);
  // checkedPurposes: { [purpose]: boolean } — 驱动三态 Switch；与 form.purposes 双向同步
  const [checkedPurposes, setCheckedPurposes] = useState({});
  // stale probe 防竞态：每次触发探测分配一个递增序号，只接受最新序号的结果
  const probeSeqRef = useRef(0);
  // 防抖定时器
  const probeTimerRef = useRef(null);
  // 正在进行的探测 Promise（保存时等它）
  const pendingProbeRef = useRef(null);

  const isEdit = !!record;

  // 打开时初始化表单
  useEffect(() => {
    if (open && type) {
      form.resetFields();
      setTestResult(null);
      setProbeResult(null);
      setProbing(false);
      probeSeqRef.current += 1; // 使所有 in-flight 探测失效（切 type 或重新打开）
      const values = isEdit ? getEditValues(type, record) : getEmptyValues(type);
      form.setFieldsValue(values);
      // 初始化 checkedPurposes（编辑时从 record 读，新建时用默认）
      const initPurposes = (isEdit && Array.isArray(record?.purposes) && record.purposes.length)
        ? record.purposes
        : (getEmptyValues(type).purposes || []);
      const initChecked = {};
      (TYPE_PURPOSES[type] || []).forEach((p) => {
        initChecked[p] = Array.isArray(initPurposes) && initPurposes.includes(p);
      });
      setCheckedPurposes(initChecked);
    }
    // 关闭时清除防抖定时器
    if (!open) {
      if (probeTimerRef.current) clearTimeout(probeTimerRef.current);
    }
  }, [open, type, record, form, isEdit]);

  // checkedPurposes 变化时同步回 form 字段
  useEffect(() => {
    const enabled = Object.entries(checkedPurposes)
      .filter(([, v]) => v)
      .map(([k]) => k);
    form.setFieldValue('purposes', enabled);
  }, [checkedPurposes, form]);

  // 构建探测 payload（与 buildTestPayload 完全相同的字段，类型一致）
  const buildProbePayload = useCallback((values) => {
    return buildTestPayload(type, values, record?.id);
  }, [type, record]);

  // 触发一次探测（可手动调用，也由防抖触发）。返回 Promise，resolve 出本次探测结果，
  // 保存时要靠它等待「防抖窗口内点了保存」的那次探测跑完（见 handleSave 的抢跑处理）。
  const triggerProbe = useCallback(() => {
    const values = form.getFieldsValue();
    // 简单校验：缺关键连接字段时不发请求
    let hasConn = true;
    if (type === 'kafka' && !textToBrokers(values.brokersText).length) hasConn = false;
    if (type === 'mongodb' && !(values.uri || '').trim()) hasConn = false;
    if (type === 'mysql' && !textToNodes(values.nodesText).length) hasConn = false;
    if (type === 'redis' && !textToNodes(values.nodesText).length) hasConn = false;
    if (type === 'elasticsearch' && !textToNodes(values.nodesText).length) hasConn = false;
    if (type === 'host' && !(values.address || '').trim()) hasConn = false;
    if (!hasConn) return Promise.resolve(null);

    probeSeqRef.current += 1;
    const mySeq = probeSeqRef.current;
    setProbing(true);
    setProbeResult(null);
    const p = probeDataSource(buildProbePayload(values))
      .then(({ data }) => {
        // 忽略过时的结果
        if (mySeq !== probeSeqRef.current) return null;
        setProbeResult(data || null);
        // 探测完成后：把不可用的用途强制关掉
        if (data?.purposes) {
          setCheckedPurposes((prev) => {
            const next = { ...prev };
            Object.entries(data.purposes).forEach(([p2, cap]) => {
              if (!cap.available) next[p2] = false;
            });
            return next;
          });
        }
        return data || null;
      })
      .catch(() => {
        if (mySeq !== probeSeqRef.current) return null;
        // 探测请求本身失败（后端 500 / 网络抖动）≠ 目标连不上。
        // 此前一律记成 connect.ok=false，等于替用户下了个「你的配置有问题」的错误结论。
        const r = { unavailable: true, purposes: {} };
        setProbeResult(r);
        return r;
      })
      .finally(() => {
        if (mySeq !== probeSeqRef.current) return;
        setProbing(false);
      });
    pendingProbeRef.current = p;
    return p;
  }, [type, form, buildProbePayload]);

  // 连接字段一变，上一次探测的结论就作废了——这里只负责清掉它，不发起新探测。
  //
  // 原先是 600ms 防抖后自动探：用户地址还没输完（比如刚打到 10.206.0.6、端口
  // 还没敲）就去连，必然失败，顶部红框 Alert 随之出现又消失，把弹窗内容撑得
  // 上下跳，用途开关也一并被标成「不可用」——填一条数据源要被警告好几轮。
  // 探测统一交给「测试连接」「重新检测」和保存前那一次：那三处才是用户认为
  // 「我填完了」的时刻。
  const resetProbe = useCallback(() => {
    if (probeTimerRef.current) clearTimeout(probeTimerRef.current);
    probeSeqRef.current += 1; // 让仍在飞的探测失效，别让它覆盖刚清空的状态
    setProbeResult(null);
  }, []);

  // purpose switch 变更处理
  const handlePurposeChange = useCallback((purpose, value) => {
    setCheckedPurposes((prev) => ({ ...prev, [purpose]: value }));
  }, []);

  const handleTest = () => {
    const values = form.getFieldsValue();
    // 基础检验：不同类型检验不同必填项
    let missingMsg = null;
    if (type === 'kafka' && !textToBrokers(values.brokersText).length) missingMsg = _l('请先输入 Broker 地址');
    if (type === 'mongodb' && !(values.uri || '').trim()) missingMsg = _l('请先输入 MongoDB 连接地址');
    if (type === 'mysql' && !textToNodes(values.nodesText).length) missingMsg = _l('请先输入节点地址');
    if (type === 'redis' && !textToNodes(values.nodesText).length) missingMsg = _l('请先输入节点地址');
    if (type === 'elasticsearch' && !textToNodes(values.nodesText).length) missingMsg = _l('请先输入 URL');
    if (missingMsg) { setTestResult({ ok: false, message: missingMsg }); return; }

    setTesting(true);
    setTestResult(null);
    testDataSource(buildTestPayload(type, values, record?.id))
      .then(({ data }) => {
        // 后端返回的是 {ok,key,detail}，从来没有 message 字段——原先读 data.message 恒为空串，
        // 于是失败时渲染成「一个红色感叹号 + 没有任何文字」，用户完全不知道错在哪
        // （ES 密码写错就是这个表现，实际 detail 里写着 401 unable to authenticate user）。
        const ok = !!(data && data.ok);
        setTestResult({
          ok,
          message: ok
            ? _l('连接成功')
            : apiErr({ response: { data } }, _l('连接失败')),
        });
      })
      .catch(() => setTestResult({ ok: false, message: _l('测试请求失败') }))
      .finally(() => setTesting(false));
  };

  const handleSave = () => {
    form.validateFields().then(async (values) => {
      // 额外校验 brokers/nodes 数组非空
      if (type === 'kafka' && !textToBrokers(values.brokersText).length) {
        message.error(_l('请至少输入一个 Broker 地址'));
        return;
      }
      if (type === 'mysql' && !textToNodes(values.nodesText).length) {
        message.error(_l('请至少输入一个节点地址'));
        return;
      }

      // ── 保存前探测：只保证「用户知道」，不替用户否决 ─────────────────────────
      //
      // 旧行为是硬拦截：连不通就不让存。但连不通 ≠ 配置错——目标还没部署、隔离网段
      // 暂时探不到都是合法场景，硬拦截会把用户卡死在一个他无从判断的地方（只能来找我们）。
      // 现在改成：第一次发现不通时把原因说清楚，保存按钮变成「仍然保存」，再点一次即存；
      // 存进去后「采集状态」列会持续显示红标和原因，通了自动转绿，用户全程只需盯一个地方。
      let vals = values;
      if (TYPE_HAS_PROBE.includes(type)) {
        const hadResult = !!probeResult;
        // 抢跑竞态：填完最后一个字段后 600ms 内点保存，防抖还没到点，
        // probing=false && probeResult=null，旧的三道闸一条都不命中，
        // 这条源等于完全没校验过就存进去了——正是「有问题的连接传给 exporter」的漏网路径。
        let probe = probeResult;
        if (probeTimerRef.current) {
          clearTimeout(probeTimerRef.current);
          probeTimerRef.current = null;
          setSaving(true);
          probe = await triggerProbe();
          setSaving(false);
        } else if (probing && pendingProbeRef.current) {
          setSaving(true);
          probe = await pendingProbeRef.current;
          setSaving(false);
        }

        if (probe?.connect && probe.connect.ok === false && !hadResult) {
          // 刚刚才探出来不通、用户还没看见过——先让红条渲染出来，这一次不存
          return;
        }
        // 不可用的用途从提交内容里剔除：triggerProbe 里的自动取消勾选走的是 React state，
        // 在「等探测→立即保存」这条路径上还没回流到 form，此处按探测结果直接兜一层。
        if (probe?.purposes) {
          vals = {
            ...vals,
            purposes: (vals.purposes || []).filter((p) => probe.purposes[p]?.available !== false),
          };
        }
      }

      // ── 重复地址软提示 ─────────────────────────────────────────────────────
      // 提取当前表单的地址摘要，在已有列表中找同地址（排除自身 id）
      const doSave = () => {
        setSaving(true);
        const payload = buildPayload(type, vals, isEdit);
        callSaveApi(type, record?.id, payload)
          .then(() => {
            message.success(isEdit ? _l('更新成功') : _l('创建成功'));
            onClose();
            if (onSaved) onSaved();
          })
          .catch((e) => message.error(apiErr(e, isEdit ? '更新失败，请重试' : '创建失败，请重试')))
          .finally(() => setSaving(false));
      };

      // 判定交给后端：同实例看的是完整连接标识（地址 + 库名 + 账号 + db 序号），
      // 只比地址端口会把「同一台 MySQL 的两个库」「同一台 Redis 的 db0/db1」误判成重复。
      // 检测失败不阻断保存——这只是个提示，不该成为新的卡点。
      checkSourceDuplicate(buildTestPayload(type, vals, record?.id))
        .then(({ data }) => {
          const dup = (data && data.duplicates && data.duplicates[0]) || null;
          if (!dup) { doSave(); return; }

          const purposeText = (dup.purposes || []).map((p) => PURPOSE_LABEL[p] || p).join('、');
          Modal.confirm({
            title: _l('这个实例已经添加过了'),
            width: 480,
            content: (
              <div>
                <div style={{ marginBottom: 8 }}>
                  {_l('已存在数据源「%0」%1，指向同一个实例。', dup.name, purposeText ? _l('（用途：%0）', purposeText) : '')}
                </div>
                <div style={{ color: 'var(--color-text-secondary, #757575)' }}>
                  {_l('如果你是想让它出现在别的功能里（看图 / 告警 / 慢查询诊断），改已有那条的「用途」就够了，不用再建一条。')}
                </div>
              </div>
            ),
            okText: _l('去补用途'),
            cancelText: _l('取消'),
            // 第三条路：确实要建两条（同地址不同用法）时放行
            okCancel: true,
            onOk: () => {
              onClose();
              if (onEditOther) onEditOther(dup);
            },
            footer: (_, { OkBtn, CancelBtn }) => (
              <>
                <CancelBtn />
                <Button onClick={() => { Modal.destroyAll(); doSave(); }}>{_l('仍然新建')}</Button>
                <OkBtn />
              </>
            ),
          });
        })
        .catch(() => doSave());
    });
  };

  const renderFields = () => {
    // 三态用途区组件（仅 kafka/mongodb/mysql 渲染；redis/es/host 用途固定不渲染）
    const ps = ['kafka', 'mongodb', 'mysql'].includes(type) ? (
      <PurposeSection
        type={type}
        probing={probing}
        probeResult={probeResult}
        checked={checkedPurposes}
        onChange={handlePurposeChange}
      />
    ) : null;

    const hasSecret = !!record?.detail?.hasSecret;

    switch (type) {
      case 'host': return <HostFields />;
      case 'kafka': return <KafkaFields editing={isEdit} hasSecret={hasSecret} form={form} purposeSection={ps} />;
      case 'mongodb': return <MongoFields editing={isEdit} hasSecret={hasSecret} form={form} purposeSection={ps} />;
      case 'mysql': return <MySQLFields editing={isEdit} hasSecret={hasSecret} form={form} purposeSection={ps} />;
      case 'redis': return <RedisFields editing={isEdit} hasSecret={hasSecret} form={form} />;
      case 'elasticsearch': return <EsFields editing={isEdit} hasSecret={hasSecret} />;
      default: return null;
    }
  };

  // 探测结论：连不通时不阻断保存，只把按钮文案换成「仍然保存」——
  // 用按钮自己说明白「我知道它现在不通，我就是要存」，比弹一个用户答不上来的确认框强。
  const connFailed = !!(probeResult?.connect && probeResult.connect.ok === false);
  const probeUnavailable = !!probeResult?.unavailable;
  // 后端 CapResult.warn 此前前端一处都没渲染。K8s 集群「部分抓取目标不可达」正是靠它带出来的
  // （集群滚动升级时个别节点探不到很正常，不该算失败，但必须让用户看见是哪几个）。
  const probeWarn = probeResult?.purposes
    ? Object.values(probeResult.purposes).map((c) => c?.warn).filter(Boolean).join('；')
    : '';

  const footer = (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
      {TYPE_HAS_TEST.includes(type) ? (
        <Space>
          <Button loading={testing} onClick={handleTest}>{_l('测试连接')}</Button>
          <Button
            icon={<SyncOutlined spin={probing} />}
            loading={probing}
            onClick={triggerProbe}
          >
            {_l('重新检测')}
          </Button>
        </Space>
      ) : <span />}
      <Space>
        <Button onClick={onClose}>{_l('取消')}</Button>
        <Button type="primary" loading={saving} onClick={handleSave}>
          {connFailed ? _l('仍然保存') : _l('保存')}
        </Button>
      </Space>
    </div>
  );

  return (
    <Modal
      title={
        <SourceTypeTitle type={type}>
          {modalTitle(type, isEdit)}
        </SourceTypeTitle>
      }
      open={open}
      onCancel={onClose}
      width={640}
      maskClosable={false}
      destroyOnClose
      footer={footer}
    >
      <Form
        form={form}
        layout="vertical"
        onValuesChange={(changedValues) => {
          // 连接字段变化时作废上一次探测结论（排除名称和用途字段），
          // 不在输入过程中发探测——见 resetProbe 的注释
          const connFields = ['uri', 'brokersText', 'nodesText', 'host', 'port', 'url',
            'username', 'password', 'authDb', 'tls', 'skipSSL', 'db',
            'saslEnable', 'saslMechanism', 'saslUsername', 'saslPassword',
            'architecture', 'address',
            'ksmTargets', 'nodeTargets', 'kubeletTargets', 'bearerToken'];
          const hasConnChange = Object.keys(changedValues).some((k) => connFields.includes(k));
          if (hasConnChange) resetProbe();
        }}
      >
        {/* 隐藏的 purposes 字段，由 checkedPurposes 状态同步写入，供 buildPayload 读取 */}
        <Form.Item name="purposes" hidden>
          <Select mode="multiple" />
        </Form.Item>
        {/* 探测结论一律置顶：Modal 内容区是可滚动的，放在字段之后会落到折叠线以下
            （实测 top=797 而可视区仅 594 高，用户完全看不见），
            那就等于「话说了但没人看得到」，方案 C 最关键的一环失效 */}
        {connFailed && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message={apiErr({ response: { data: probeResult.connect } }, _l('连接不通'))}
            description={_l('可以先保存。保存后会持续重试，通了会自动开始采集；在「数据源」列表的「采集状态」列可以随时看到进展。')}
          />
        )}
        {!connFailed && probeWarn && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message={probeWarn}
            description={_l('不影响保存，其余目标会正常采集。')}
          />
        )}
        {probeUnavailable && (
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
            message={_l('暂时无法检测连接')}
            description={_l('检测服务没有响应，这不代表你填的配置有问题。可以直接保存，之后看「采集状态」列。')}
          />
        )}
        {renderFields()}
        {testResult && (
          <Alert
            type={testResult.ok ? 'success' : 'error'}
            message={testResult.message}
            showIcon
            closable
            onClose={() => setTestResult(null)}
            style={{ marginTop: 8 }}
          />
        )}
      </Form>
    </Modal>
  );
};

export default SourceFormModal;
