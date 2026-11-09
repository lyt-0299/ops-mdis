/**
 * MySQLInstanceModal — MySQL 实例管理弹窗（列表 + 新建/编辑/删除）
 *
 * Props:
 *   open        {boolean}   controlled visibility
 *   onClose     {Function}  close callback
 *   onRefresh   {Function}  () => void — called after any mutation so parent can reload
 *
 * MySQLInstance fields (camelCase, per model):
 *   id, name, architecture(standalone/replication/mgr), nodes[], username, password,
 *   database, tls, skipSSL, createdAt, updatedAt
 */
import React, { useEffect, useState } from 'react';
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
  createMysqlInstance,
  deleteMysqlInstance,
  listMysqlInstances,
  testDataSource,
  updateMysqlInstance,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

const { TextArea } = Input;
const { Option } = Select;

const ARCHITECTURE_OPTIONS = [
  { value: 'standalone', label: _l('单机') },
  { value: 'replication', label: _l('主从复制') },
  { value: 'mgr', label: _l('MGR（组复制）') },
];

// 数据源用途选项：看图(metrics) / 告警(alert)
const PURPOSE_OPTIONS = [
  { value: 'metrics', label: _l('看图') },
  { value: 'alert', label: _l('告警') },
];
const DEFAULT_PURPOSES = ['metrics', 'alert'];

const nodesToText = (nodes) =>
  Array.isArray(nodes) ? nodes.join('\n') : (nodes || '');

const textToNodes = (text) =>
  (text || '')
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);

const emptyForm = () => ({
  name: '',
  architecture: 'standalone',
  nodesText: '',
  username: '',
  password: '',
  database: 'mysql',
  tls: false,
  skipSSL: false,
  purposes: DEFAULT_PURPOSES,
});

const MySQLInstanceModal = ({ open, onClose, onRefresh }) => {
  const [instances, setInstances] = useState([]);
  const [loading, setLoading] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState(null); // null = create
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null); // { ok, message }
  const [form] = Form.useForm();
  const tlsEnabled = Form.useWatch('tls', form);

  const handleTest = () => {
    const values = form.getFieldsValue();
    const nodes = textToNodes(values.nodesText);
    if (!nodes.length) {
      setTestResult({ ok: false, message: _l('请先输入节点地址') });
      return;
    }
    setTesting(true);
    setTestResult(null);
    testDataSource({
      type: 'mysql',
      id: editing?.id,
      nodes,
      username: values.username?.trim() || '',
      password: values.password || '',
      database: values.database?.trim() || '',
      tls: values.tls || false,
      skipSSL: values.skipSSL || false,
    })
      .then(({ data }) => {
        setTestResult({ ok: !!(data && data.ok), message: (data && data.message) || '' });
      })
      .catch(() => setTestResult({ ok: false, message: _l('测试请求失败') }))
      .finally(() => setTesting(false));
  };

  const fetchList = () => {
    setLoading(true);
    listMysqlInstances()
      .then(({ data }) => setInstances(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 MySQL 实例列表失败'))))
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
      architecture: record.architecture || 'standalone',
      nodesText: nodesToText(record.nodes),
      username: record.username || '',
      password: '', // never pre-fill password
      database: record.database || 'mysql',
      tls: record.tls || false,
      skipSSL: record.skipSSL || false,
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
      const nodes = textToNodes(values.nodesText);
      if (!nodes.length) {
        message.error(_l('请至少输入一个节点地址'));
        setConfirmLoading(false);
        return;
      }
      const payload = {
        name: values.name.trim(),
        architecture: values.architecture || 'standalone',
        nodes,
        username: values.username?.trim() || '',
        database: values.database?.trim() || 'mysql',
        tls: values.tls || false,
        skipSSL: values.skipSSL || false,
        purposes: Array.isArray(values.purposes) ? values.purposes : DEFAULT_PURPOSES,
      };
      if (values.password) {
        payload.password = values.password;
      }
      const req = editing
        ? updateMysqlInstance(editing.id, payload)
        : createMysqlInstance(payload);
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
    deleteMysqlInstance(record.id)
      .then(() => {
        message.success(_l('删除成功'));
        fetchList();
        if (onRefresh) onRefresh();
      })
      .catch((e) => message.error(apiErr(e, _l('删除失败，请重试'))));
  };

  const archLabel = (arch) => {
    const opt = ARCHITECTURE_OPTIONS.find((o) => o.value === arch);
    return opt ? opt.label : arch || '-';
  };

  const columns = [
    {
      title: _l('名称'),
      dataIndex: 'name',
      ellipsis: true,
      render: (v) => <span style={{ fontWeight: 500 }}>{v}</span>,
    },
    {
      title: _l('架构'),
      dataIndex: 'architecture',
      width: 120,
      render: (v) => {
        const colorMap = { standalone: 'default', replication: 'blue', mgr: 'purple' };
        return <Tag color={colorMap[v] || 'default'}>{archLabel(v)}</Tag>;
      },
    },
    {
      title: _l('节点'),
      dataIndex: 'nodes',
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
      title: 'TLS',
      width: 80,
      render: (_, r) =>
        r.tls ? (
          <Tag color="blue">TLS</Tag>
        ) : (
          <span style={{ color: '#ccc' }}>-</span>
        ),
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
          <SourceTypeTitle type="mysql">
            {_l('管理 MySQL 实例')}
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
        width={760}
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
          locale={{ emptyText: _l('暂无 MySQL 实例，点击「新建实例」添加') }}
        />
      </Modal>

      {/* Create / Edit sub-modal */}
      <Modal
        title={
          <SourceTypeTitle type="mysql">
            {editing ? _l('编辑 MySQL 实例') : _l('新建 MySQL 实例')}
          </SourceTypeTitle>
        }
        open={editOpen}
        width={560}
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
            <Input placeholder={_l('例如：生产环境 MySQL 主从')} />
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

          <Form.Item label={_l('用户名 / 密码')} style={{ marginBottom: 0 }}>
            <Space style={{ display: 'flex' }}>
              <Form.Item name="username" noStyle>
                <Input placeholder={_l('用户名')} autoComplete="off" style={{ width: 200 }} />
              </Form.Item>
              <Form.Item name="password" noStyle>
                <Input.Password
                  placeholder={editing ? _l('留空不修改') : _l('密码')}
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

export default MySQLInstanceModal;
