/**
 * KafkaInstanceModal — Kafka 实例管理弹窗（列表 + 新建/编辑/删除）
 *
 * Props:
 *   open        {boolean}   controlled visibility
 *   onClose     {Function}  close callback
 *   instances   {Array}     current list (managed externally so both pages share state)
 *   onRefresh   {Function}  () => void — called after any mutation so parent can reload
 */
import React, { useEffect,useState } from 'react';
import {
  PlusOutlined,
} from '@ant-design/icons';
import {
  Alert,
  Button,
  Divider,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import { SourceTypeTitle } from '@/components/SourceTypeLogo';
import {
  createKafkaInstance,
  deleteKafkaInstance,
  listKafkaInstances,
  testDataSource,
  updateKafkaInstance,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

const { TextArea } = Input;
const { Option } = Select;

const SASL_MECHANISMS = [
  { value: 'PLAIN', label: 'PLAIN' },
  { value: 'SCRAM-SHA-256', label: 'SCRAM-SHA-256' },
  { value: 'SCRAM-SHA-512', label: 'SCRAM-SHA-512' },
];

// 数据源用途选项：看图(metrics) / 告警(alert)
const PURPOSE_OPTIONS = [
  { value: 'metrics', label: _l('看图') },
  { value: 'alert', label: _l('告警') },
];
const DEFAULT_PURPOSES = ['metrics', 'alert'];

// Convert brokers array ↔ textarea string
const brokersToText = (brokers) =>
  Array.isArray(brokers) ? brokers.join('\n') : (brokers || '');

const textToBrokers = (text) =>
  (text || '')
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);

const emptyForm = () => ({
  name: '',
  brokersText: '',
  saslEnable: false,
  saslMechanism: 'PLAIN',
  saslUsername: '',
  saslPassword: '',
  purposes: DEFAULT_PURPOSES,
});

const KafkaInstanceModal = ({ open, onClose, onRefresh }) => {
  const [instances, setInstances]   = useState([]);
  const [loading, setLoading]       = useState(false);
  const [editOpen, setEditOpen]     = useState(false);
  const [editing, setEditing]       = useState(null); // null = create
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null); // { ok, message }
  const [form] = Form.useForm();
  const saslEnable = Form.useWatch('saslEnable', form);

  const handleTest = () => {
    const values = form.getFieldsValue();
    const brokers = textToBrokers(values.brokersText);
    if (!brokers.length) {
      setTestResult({ ok: false, message: _l('请先输入 Broker 地址') });
      return;
    }
    setTesting(true);
    setTestResult(null);
    testDataSource({
      type: 'kafka',
      id: editing?.id,
      brokers,
      sasl: values.saslEnable
        ? {
            enabled: true,
            mechanism: values.saslMechanism,
            username: values.saslUsername,
            password: values.saslPassword || '',
          }
        : { enabled: false },
    })
      .then(({ data }) => {
        setTestResult({ ok: !!(data && data.ok), message: (data && data.message) || '' });
      })
      .catch(() => setTestResult({ ok: false, message: _l('测试请求失败') }))
      .finally(() => setTesting(false));
  };

  const fetchList = () => {
    setLoading(true);
    listKafkaInstances()
      .then(({ data }) => setInstances(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 Kafka 实例列表失败'))))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    if (open) fetchList();
  }, [open]);

  const openCreate = () => {
    setEditing(null);
    setTestResult(null);
    form.resetFields();
    form.setFieldsValue(emptyForm());
    setEditOpen(true);
  };

  const openEdit = (record) => {
    setEditing(record);
    setTestResult(null);
    form.resetFields();
    form.setFieldsValue({
      name: record.name,
      brokersText: brokersToText(record.brokers),
      saslEnable: record.sasl?.enabled || false,
      saslMechanism: record.sasl?.mechanism || 'PLAIN',
      saslUsername: record.sasl?.username || '',
      saslPassword: '', // never pre-fill password
      purposes: Array.isArray(record.purposes) && record.purposes.length
        ? record.purposes
        : DEFAULT_PURPOSES,
    });
    setEditOpen(true);
  };

  const closeEdit = () => {
    setEditOpen(false);
    setEditing(null);
    setTestResult(null);
    form.resetFields();
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);
      const brokers = textToBrokers(values.brokersText);
      if (!brokers.length) {
        message.error(_l('请至少输入一个 Broker 地址'));
        setConfirmLoading(false);
        return;
      }
      const payload = {
        name: values.name.trim(),
        brokers,
        purposes: Array.isArray(values.purposes) ? values.purposes : DEFAULT_PURPOSES,
      };
      if (values.saslEnable) {
        payload.sasl = {
          enabled: true,
          mechanism: values.saslMechanism,
          username: values.saslUsername,
          password: values.saslPassword,
        };
      } else {
        payload.sasl = { enabled: false };
      }
      const req = editing
        ? updateKafkaInstance(editing.id, payload)
        : createKafkaInstance(payload);
      req
        .then(() => {
          message.success(editing ? _l('更新成功') : _l('创建成功'));
          closeEdit();
          fetchList();
          if (onRefresh) onRefresh();
        })
        .catch(() => message.error(editing ? _l('更新失败，请重试') : _l('创建失败，请重试')))
        .finally(() => setConfirmLoading(false));
    }).catch(() => setConfirmLoading(false));
  };

  const handleDelete = (record) => {
    deleteKafkaInstance(record.id)
      .then(() => {
        message.success(_l('删除成功'));
        fetchList();
        if (onRefresh) onRefresh();
      })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  const columns = [
    {
      title: _l('名称'),
      dataIndex: 'name',
      ellipsis: true,
      render: (v) => <span style={{ fontWeight: 500 }}>{v}</span>,
    },
    {
      title: 'Brokers',
      dataIndex: 'brokers',
      ellipsis: true,
      render: (v) => {
        const list = Array.isArray(v) ? v : [];
        return (
          <span style={{ fontFamily: 'monospace', fontSize: 12, color: '#555' }}>
            {list.join(', ') || '-'}
          </span>
        );
      },
    },
    {
      title: 'SASL',
      width: 120,
      render: (_, r) =>
        r.sasl?.enabled
          ? <Tag color="blue">{r.sasl.mechanism || 'PLAIN'}</Tag>
          : <span style={{ color: '#ccc' }}>-</span>,
    },
    {
      title: _l('操作'),
      width: 90,
      render: (_, record) => (
        <span className="opsActionGroup">
          <ActionIcon type="edit" title={_l('编辑')} onClick={() => openEdit(record)} />
          <Popconfirm
            title={_l('确认删除')}
            description={_l('删除后不可恢复，确认继续？')}
            okText={_l('删除')}
            okType="danger"
            cancelText={_l('取消')}
            onConfirm={() => handleDelete(record)}
          >
            <span>
              <ActionIcon type="delete" title={_l('删除')} danger />
            </span>
          </Popconfirm>
        </span>
      ),
    },
  ];

  return (
    <>
      {/* Main manager modal */}
      <Modal
        title={
          <SourceTypeTitle type="kafka">
            {_l('管理 Kafka 实例')}
          </SourceTypeTitle>
        }
        open={open}
        onCancel={onClose}
        footer={
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              {_l('新建实例')}
            </Button>
            <Button onClick={onClose}>{_l('关闭')}</Button>
          </div>
        }
        width={680}
        destroyOnClose
      >
        <Table
          loading={loading}
          columns={columns}
          dataSource={instances}
          rowKey="id"
          pagination={false}
          size="small"
          style={{ marginTop: 8 }}
          locale={{ emptyText: _l('暂无 Kafka 实例，点击「新建实例」添加') }}
        />
      </Modal>

      {/* Create / Edit sub-modal */}
      <Modal
        title={
          <SourceTypeTitle type="kafka">
            {editing ? _l('编辑 Kafka 实例') : _l('新建 Kafka 实例')}
          </SourceTypeTitle>
        }
        open={editOpen}
        width={520}
        maskClosable={false}
        confirmLoading={confirmLoading}
        onCancel={closeEdit}
        destroyOnClose
        footer={
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <Button loading={testing} onClick={handleTest}>{_l('测试连接')}</Button>
            <Space>
              <Button onClick={closeEdit}>{_l('取消')}</Button>
              <Button type="primary" loading={confirmLoading} onClick={handleSave}>
                {_l('保存')}
              </Button>
            </Space>
          </div>
        }
      >
        <Form form={form} layout="vertical" style={{ marginTop: 8 }}>
          <Form.Item
            name="name"
            label={_l('实例名称')}
            rules={[{ required: true, message: _l('请输入实例名称') }]}
          >
            <Input placeholder={_l('例如：生产环境 Kafka')} />
          </Form.Item>

          <Form.Item
            name="purposes"
            label={_l('用途')}
            extra={_l('看图：用于 Grafana 等图表数据源；告警：纳入告警监控')}
          >
            <Select mode="multiple" allowClear placeholder={_l('请选择用途')}>
              {PURPOSE_OPTIONS.map((o) => (
                <Option key={o.value} value={o.value}>{o.label}</Option>
              ))}
            </Select>
          </Form.Item>

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
                extra={editing ? _l('留空表示不修改密码') : ''}
                rules={editing ? [] : [{ required: true, message: _l('请输入密码') }]}
              >
                <Input.Password autoComplete="new-password" />
              </Form.Item>
            </>
          )}

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
    </>
  );
};

export default KafkaInstanceModal;
