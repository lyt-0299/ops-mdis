/**
 * HostInstanceModal — 主机实例管理弹窗（列表 + 新建/编辑/删除）
 *
 * Props:
 *   open        {boolean}   controlled visibility
 *   onClose     {Function}  close callback
 *   onRefresh   {Function}  () => void — called after any mutation so parent can reload
 *
 * 主机仅用于看图（metrics），通过 node_exporter 地址采集主机指标。
 * purposes 锁定 ["metrics"]，不在表单中显示。无密码 / 无连接测试。
 */
import React, { useEffect, useState } from 'react';
import {
  PlusOutlined,
} from '@ant-design/icons';
import {
  Button,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Space,
  Table,
} from 'antd';

import ActionIcon from '@/components/ActionIcon';
import { SourceTypeTitle } from '@/components/SourceTypeLogo';
import {
  createHostInstance,
  deleteHostInstance,
  listHostInstances,
  updateHostInstance,
} from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

// 主机仅看图
const HOST_PURPOSES = ['metrics'];

// host:port 校验（node_exporter 地址）
const ADDRESS_PATTERN = /^[^\s:]+:\d{1,5}$/;

const emptyForm = () => ({
  name: '',
  address: '',
});

const HostInstanceModal = ({ open, onClose, onRefresh }) => {
  const [instances, setInstances] = useState([]);
  const [loading, setLoading] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState(null); // null = create
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();

  const fetchList = () => {
    setLoading(true);
    listHostInstances()
      .then(({ data }) => setInstances(Array.isArray(data) ? data : []))
      .catch((e) => message.error(apiErr(e, _l('获取主机实例列表失败'))))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    if (open) fetchList();
  }, [open]);

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue(emptyForm());
    setEditOpen(true);
  };

  const openEdit = (record) => {
    setEditing(record);
    form.resetFields();
    form.setFieldsValue({
      name: record.name,
      address: record.address || '',
    });
    setEditOpen(true);
  };

  const closeEdit = () => {
    setEditOpen(false);
    setEditing(null);
    form.resetFields();
  };

  const handleSave = () => {
    form.validateFields().then((values) => {
      setConfirmLoading(true);
      const payload = {
        name: values.name.trim(),
        address: values.address.trim(),
        purposes: HOST_PURPOSES,
      };
      const req = editing
        ? updateHostInstance(editing.id, payload)
        : createHostInstance(payload);
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
    deleteHostInstance(record.id)
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
      title: _l('地址'),
      dataIndex: 'address',
      ellipsis: true,
      render: (v) => (
        <span style={{ fontFamily: 'monospace', fontSize: 12, color: '#555' }}>
          {v || '-'}
        </span>
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
          <SourceTypeTitle type="host">
            {_l('管理主机实例')}
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
          locale={{ emptyText: _l('暂无主机实例，点击「新建实例」添加') }}
        />
      </Modal>

      {/* Create / Edit sub-modal */}
      <Modal
        title={
          <SourceTypeTitle type="host">
            {editing ? _l('编辑主机实例') : _l('新建主机实例')}
          </SourceTypeTitle>
        }
        open={editOpen}
        width={520}
        maskClosable={false}
        confirmLoading={confirmLoading}
        onCancel={closeEdit}
        destroyOnClose
        footer={
          <div style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center' }}>
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
            <Input placeholder={_l('例如：生产环境 50 主机')} />
          </Form.Item>

          <Form.Item
            name="address"
            label={_l('node_exporter 地址')}
            rules={[
              { required: true, message: _l('请输入 node_exporter 地址') },
              {
                pattern: ADDRESS_PATTERN,
                message: _l('格式应为 host:port，例如 172.17.30.50:59100'),
              },
            ]}
            extra={_l('node_exporter 暴露的地址，格式 host:port')}
          >
            <Input
              placeholder="172.17.30.50:59100"
              style={{ fontFamily: 'monospace', fontSize: 12 }}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};

export default HostInstanceModal;
