/**
 * EsInstanceModal — Elasticsearch 实例管理弹窗（列表 + 新建/编辑/删除）
 *
 * Props:
 *   open        {boolean}   controlled visibility
 *   onClose     {Function}  close callback
 *   onRefresh   {Function}  () => void — called after any mutation so parent can reload
 *
 * Elasticsearch 仅用于看图（metrics），purposes 锁定 ["metrics"]，不在表单中显示。
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
  Space,
  Switch,
  Table,
  Tag,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import { SourceTypeTitle } from '@/components/SourceTypeLogo';
import {
  createEsInstance,
  deleteEsInstance,
  listEsInstances,
  testDataSource,
  updateEsInstance,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

// ES 仅看图
const ES_PURPOSES = ['metrics'];

const emptyForm = () => ({
  name: '',
  url: '',
  username: '',
  password: '',
  skipSSL: false,
});

const EsInstanceModal = ({ open, onClose, onRefresh }) => {
  const [instances, setInstances] = useState([]);
  const [loading, setLoading] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState(null); // null = create
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null); // { ok, message }
  const [form] = Form.useForm();

  const fetchList = () => {
    setLoading(true);
    listEsInstances()
      .then(({ data }) => setInstances(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取 Elasticsearch 实例列表失败'))))
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
      url: record.url || '',
      username: record.username || '',
      password: '', // never pre-fill password
      skipSSL: record.skipSSL || false,
    });
    setEditOpen(true);
  };

  const closeEdit = () => {
    setEditOpen(false);
    setEditing(null);
    setTestResult(null);
    form.resetFields();
  };

  const handleTest = () => {
    const values = form.getFieldsValue();
    const url = (values.url || '').trim();
    if (!url) {
      setTestResult({ ok: false, message: _l('请先输入 URL') });
      return;
    }
    setTesting(true);
    setTestResult(null);
    testDataSource({
      type: 'elasticsearch',
      id: editing?.id,
      url,
      username: values.username?.trim() || '',
      password: values.password || '',
      skipSSL: values.skipSSL || false,
    })
      .then(({ data }) => {
        setTestResult({ ok: !!(data && data.ok), message: (data && data.message) || '' });
      })
      .catch(() => setTestResult({ ok: false, message: _l('测试请求失败') }))
      .finally(() => setTesting(false));
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);
      const payload = {
        name: values.name.trim(),
        url: values.url.trim(),
        username: values.username?.trim() || '',
        skipSSL: values.skipSSL || false,
        purposes: ES_PURPOSES,
      };
      if (values.password) {
        payload.password = values.password;
      }
      const req = editing
        ? updateEsInstance(editing.id, payload)
        : createEsInstance(payload);
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
    deleteEsInstance(record.id)
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
      title: 'URL',
      dataIndex: 'url',
      ellipsis: true,
      render: (v) => (
        <span style={{ fontFamily: 'monospace', fontSize: 12, color: '#555' }}>
          {v || '-'}
        </span>
      ),
    },
    {
      title: _l('用户名'),
      dataIndex: 'username',
      width: 140,
      render: (v) => v || <span style={{ color: '#ccc' }}>-</span>,
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
          <SourceTypeTitle type="elasticsearch">
            {_l('管理 Elasticsearch 实例')}
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
        width={720}
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
          locale={{ emptyText: _l('暂无 Elasticsearch 实例，点击「新建实例」添加') }}
        />
      </Modal>

      {/* Create / Edit sub-modal */}
      <Modal
        title={
          <SourceTypeTitle type="elasticsearch">
            {editing ? _l('编辑 Elasticsearch 实例') : _l('新建 Elasticsearch 实例')}
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
            <Input placeholder={_l('例如：生产环境 Elasticsearch')} />
          </Form.Item>

          <Form.Item
            name="url"
            label="URL"
            rules={[
              { required: true, message: _l('请输入 URL') },
              {
                pattern: /^https?:\/\/[\w.-]+(:\d+)?(\/.*)?$/i,
                message: _l('请输入有效的 URL，例如 http://es:9200'),
              },
            ]}
            extra={_l('格式：http(s)://host:9200')}
          >
            <Input
              placeholder="http://es:9200"
              style={{ fontFamily: 'monospace', fontSize: 12 }}
            />
          </Form.Item>

          <Divider style={{ margin: '12px 0' }}>{_l('认证（可选）')}</Divider>

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

          <Divider style={{ margin: '12px 0' }}>TLS</Divider>

          <Form.Item
            name="skipSSL"
            label={_l('跳过 SSL 证书验证')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>

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

export default EsInstanceModal;
