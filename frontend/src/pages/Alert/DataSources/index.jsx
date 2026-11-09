import React, { useCallback,useEffect, useMemo, useState } from 'react';
import {
  PlusOutlined,
} from '@ant-design/icons';
import {
  Button,
  Empty,
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
  Tooltip,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import { SourceTypeLabel, SourceTypeTitle } from '@/components/SourceTypeLogo';
import {
  createDataSource,
  deleteDataSource,
  listDataSources,
  updateDataSource,
} from '@/utils/alertApi';
import { getSourceDisplayName } from '@/utils/sourceDisplay';
import { _l } from '@/utils/utils';

import './index.less';

const { Option } = Select;

// Auth type options
const AUTH_TYPE_OPTIONS = [
  { value: 'none', label: _l('无认证') },
  { value: 'basic', label: 'Basic Auth' },
  { value: 'bearer', label: 'Bearer Token' },
];

const AUTH_TYPE_TAG = {
  none: { color: 'default', text: _l('无认证') },
  basic: { color: 'blue', text: 'Basic Auth' },
  bearer: { color: 'purple', text: 'Bearer Token' },
};

const DataSources = () => {
  const [loading, setLoading] = useState(false);
  const [dataSource, setDataSource] = useState([]);
  const [modalVisible, setModalVisible] = useState(false);
  const [editingRecord, setEditingRecord] = useState(null); // null = create mode
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();

  // Watch authType to show/hide conditional fields
  const authType = Form.useWatch('authType', form);

  // ── Data fetching ──────────────────────────────────────────────────────────

  const fetchList = useCallback(() => {
    setLoading(true);
    listDataSources()
      .then(({ data }) => {
        setDataSource(Array.isArray(data) ? data : []);
      })
      .catch(() => {
        message.error(_l('获取数据源列表失败'));
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    fetchList();
  }, [fetchList]);

  // ── Modal open/close ───────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingRecord(null);
    form.resetFields();
    form.setFieldsValue({ authType: 'none', skipSSL: false });
    setModalVisible(true);
  };

  const openEdit = (record) => {
    setEditingRecord(record);
    form.resetFields();
    form.setFieldsValue({
      name: record.name,
      type: record.type || 'prometheus',
      url: record.url,
      authType: record.authType || 'none',
      username: record.username || '',
      // password/token are blanked by the backend list response; leave empty
      password: '',
      token: '',
      skipSSL: !!record.skipSSL,
    });
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setEditingRecord(null);
    form.resetFields();
  };

  // ── Save (create / update) ─────────────────────────────────────────────────

  const handleSave = () => {
    form
      .validateFields()
      .then((values) => {
        setConfirmLoading(true);
        const payload = {
          name: values.name,
          type: values.type || 'prometheus',
          url: values.url,
          authType: values.authType,
          skipSSL: !!values.skipSSL,
        };

        if (values.authType === 'basic') {
          payload.username = values.username || '';
          if (values.password) payload.password = values.password;
        } else if (values.authType === 'bearer') {
          if (values.token) payload.token = values.token;
        }

        const request = editingRecord
          ? updateDataSource(editingRecord.id, payload)
          : createDataSource(payload);

        request
          .then(() => {
            message.success(
              editingRecord ? _l('更新成功') : _l('创建成功')
            );
            closeModal();
            fetchList();
          })
          .catch(() => {
            message.error(
              editingRecord ? _l('更新失败，请重试') : _l('创建失败，请重试')
            );
          })
          .finally(() => {
            setConfirmLoading(false);
          });
      })
      .catch(() => {
        // form validation failed — antd shows field errors automatically
      });
  };

  // ── Delete ─────────────────────────────────────────────────────────────────

  const handleDelete = (record) => {
    deleteDataSource(record.id)
      .then(() => {
        message.success(_l('删除成功'));
        fetchList();
      })
      .catch(() => {
        message.error(_l('删除失败，请重试'));
      });
  };

  // ── Columns ────────────────────────────────────────────────────────────────

  const columns = useMemo(
    () => [
      {
        title: '',
        dataIndex: 'type',
        width: 64,
        align: 'center',
        render: (text) => (
          <SourceTypeLabel type={text || 'prometheus'} showText={false} />
        ),
      },
      {
        title: _l('名称'),
        dataIndex: 'name',
        ellipsis: true,
        render: (text, record) => (
          <span className="dsNameCell">
            {getSourceDisplayName(record)}
          </span>
        ),
      },
      {
        title: 'URL',
        dataIndex: 'url',
        ellipsis: true,
        render: (text) => (
          <Tooltip title={text}>
            <span className="dsUrlCell">{text}</span>
          </Tooltip>
        ),
      },
      {
        title: _l('认证方式'),
        dataIndex: 'authType',
        width: 160,
        render: (text) => {
          const cfg = AUTH_TYPE_TAG[text] || AUTH_TYPE_TAG['none'];
          return <Tag color={cfg.color}>{cfg.text}</Tag>;
        },
      },
      {
        title: _l('跳过SSL验证'),
        dataIndex: 'skipSSL',
        width: 130,
        render: (val) =>
          val ? (
            <Tag color="warning">{_l('是')}</Tag>
          ) : (
            <Tag>{_l('否')}</Tag>
          ),
      },
      {
        title: _l('操作'),
        width: 90,
        fixed: 'right',
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
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div className="alertDataSourcesContainer">
      {/* ── Toolbar ─────────────────────────────────────────────────── */}
      <div className="dsToolbar opsPageHeader">
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={openCreate}
        >
          {_l('新建数据源')}
        </Button>
      </div>

      {/* ── Table ───────────────────────────────────────────────────── */}
      <div className="dsTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={dataSource}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false }}
          scroll={{ x: '100%' }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('暂无数据源，点击「新建数据源」添加')}
              />
            ),
          }}
        />
      </div>

      {/* ── Create / Edit Modal ─────────────────────────────────────── */}
      <Modal
        title={
          <SourceTypeTitle type="prometheus">
            {editingRecord ? _l('编辑数据源') : _l('新建数据源')}
          </SourceTypeTitle>
        }
        open={modalVisible}
        width={600}
        maskClosable={false}
        confirmLoading={confirmLoading}
        okText={_l('保存')}
        cancelText={_l('取消')}
        onOk={handleSave}
        onCancel={closeModal}
      >
        <Form
          form={form}
          layout="vertical"
          className="dsForm"
          initialValues={{ authType: 'none', skipSSL: false }}
        >
          {/* 名称 */}
          <Form.Item
            name="name"
            label={_l('名称')}
            rules={[{ required: true, message: _l('请输入数据源名称') }]}
          >
            <Input placeholder={_l('例如：生产环境 Prometheus')} />
          </Form.Item>

          {/* 类型 (固定 prometheus 阶段，select 留作扩展) */}
          <Form.Item
            name="type"
            label={_l('类型')}
            initialValue="prometheus"
          >
            <Select disabled>
              <Option value="prometheus">
                <SourceTypeLabel type="prometheus" />
              </Option>
            </Select>
          </Form.Item>

          {/* URL */}
          <Form.Item
            name="url"
            label="URL"
            rules={[
              { required: true, message: _l('请输入 URL') },
              {
                pattern: /^https?:\/\/[\w.-]+(:\d+)?(\/.*)?$/i,
                message: _l('请输入有效的 URL，例如 http://ops-prometheus:9090'),
              },
            ]}
          >
            <Input placeholder="http://prometheus:9090" />
          </Form.Item>

          {/* 认证方式 */}
          <Form.Item name="authType" label={_l('认证方式')}>
            <Select>
              {AUTH_TYPE_OPTIONS.map((opt) => (
                <Option key={opt.value} value={opt.value}>
                  {opt.label}
                </Option>
              ))}
            </Select>
          </Form.Item>

          {/* Basic Auth — username + password */}
          {authType === 'basic' && (
            <>
              <Form.Item
                name="username"
                label={_l('用户名')}
                rules={[{ required: true, message: _l('请输入用户名') }]}
              >
                <Input autoComplete="off" placeholder="admin" />
              </Form.Item>
              <Form.Item
                name="password"
                label={_l('密码')}
                extra={editingRecord ? _l('留空表示不修改密码') : undefined}
                rules={
                  editingRecord
                    ? []
                    : [{ required: true, message: _l('请输入密码') }]
                }
              >
                <Input.Password autoComplete="new-password" />
              </Form.Item>
            </>
          )}

          {/* Bearer Token */}
          {authType === 'bearer' && (
            <Form.Item
              name="token"
              label="Token"
              extra={editingRecord ? _l('留空表示不修改 Token') : undefined}
              rules={
                editingRecord
                  ? []
                  : [{ required: true, message: _l('请输入 Bearer Token') }]
              }
            >
              <Input.Password
                autoComplete="new-password"
                placeholder={editingRecord ? '••••••••' : 'eyJhbGci...'}
              />
            </Form.Item>
          )}

          {/* 跳过 SSL 验证 */}
          <Form.Item
            name="skipSSL"
            label={_l('跳过 SSL 证书验证')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default DataSources;
