import React, { useCallback,useEffect, useMemo, useState } from 'react';
import {
  LinkOutlined,
  MailOutlined,
  NotificationOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import {
  Button,
  Empty,
  Form,
  Input,
  InputNumber,
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
import {
  createWebhook,
  deleteWebhook,
  listWebhooks,
  updateWebhook,
} from '@/utils/alertApi';
import { _l } from '@/utils/utils';

import './index.less';

const { Option } = Select;

// ── Webhook type config ────────────────────────────────────────────────────

const WEBHOOK_TYPES = [
  { value: 'wecom', label: _l('企业微信') },
  { value: 'dingtalk', label: _l('钉钉') },
  { value: 'feishu', label: _l('飞书') },
  { value: 'email', label: _l('邮件') },
  { value: 'custom', label: _l('自定义 Webhook') },
];

const WEBHOOK_TYPE_COLOR = {
  wecom: 'green',
  dingtalk: 'blue',
  feishu: 'cyan',
  email: 'orange',
  custom: 'purple',
};

// ── Empty form values ───────────────────────────────────────────────────────

const emptyForm = () => ({
  name: '',
  type: 'wecom',
  url: '',
  proxyUrl: '',
  mentionedList: '',
  mentionedMobileList: '',
  isAtAll: false,
  smtpHost: '',
  smtpPort: 465,
  smtpUser: '',
  smtpPass: '',
  skipSSL: false,
  emailTo: '',
});

// ── URL placeholders by type ────────────────────────────────────────────────

const URL_PLACEHOLDER = {
  wecom: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx',
  dingtalk: 'https://oapi.dingtalk.com/robot/send?access_token=xxx',
  feishu: 'https://open.feishu.cn/open-apis/bot/v2/hook/xxx',
  custom: 'https://your-endpoint.example.com/alert',
};

// ── Main component ─────────────────────────────────────────────────────────

const Webhooks = () => {
  const [loading, setLoading] = useState(false);
  const [webhooks, setWebhooks] = useState([]);
  const [modalVisible, setModalVisible] = useState(false);
  const [editingRecord, setEditingRecord] = useState(null);
  const [confirmLoading, setConfirmLoading] = useState(false);
  const [form] = Form.useForm();

  const webhookType = Form.useWatch('type', form);

  // ── Data fetch ────────────────────────────────────────────────────────────

  const fetchList = useCallback(() => {
    setLoading(true);
    listWebhooks()
      .then(({ data }) => {
        setWebhooks(Array.isArray(data) ? data : []);
      })
      .catch(() => {
        message.error(_l('获取通知渠道列表失败'));
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    fetchList();
  }, [fetchList]);

  // ── Modal helpers ─────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingRecord(null);
    form.resetFields();
    form.setFieldsValue(emptyForm());
    setModalVisible(true);
  };

  const openEdit = (record) => {
    setEditingRecord(record);
    form.resetFields();
    form.setFieldsValue({
      name: record.name,
      type: record.type,
      url: record.url || '',
      proxyUrl: record.proxyUrl || '',
      mentionedList: record.mentionedList || '',
      mentionedMobileList: record.mentionedMobileList || '',
      isAtAll: !!record.isAtAll,
      smtpHost: record.smtpHost || '',
      smtpPort: record.smtpPort || 465,
      smtpUser: record.smtpUser || '',
      smtpPass: '',
      skipSSL: record.skipSSL !== false,
      emailTo: record.emailTo || '',
    });
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setEditingRecord(null);
    form.resetFields();
  };

  // ── Save ──────────────────────────────────────────────────────────────────

  const handleSave = () => {
    form
      .validateFields()
      .then((values) => {
        setConfirmLoading(true);
        const payload = { ...values };

        // Remove empty optional strings
        ['proxyUrl', 'mentionedList', 'mentionedMobileList'].forEach((k) => {
          if (!payload[k]) delete payload[k];
        });

        // In edit mode, skipSSL defaults to keeping previous; smtpPass blank = no change
        if (editingRecord && !payload.smtpPass) {
          delete payload.smtpPass;
        }

        const request = editingRecord
          ? updateWebhook(editingRecord.id, payload)
          : createWebhook(payload);

        request
          .then(() => {
            message.success(editingRecord ? _l('更新成功') : _l('创建成功'));
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
        // Antd shows field-level errors automatically
      });
  };

  // ── Delete ────────────────────────────────────────────────────────────────

  const handleDelete = (record) => {
    deleteWebhook(record.id)
      .then(() => {
        message.success(_l('删除成功'));
        fetchList();
      })
      .catch(() => {
        message.error(_l('删除失败，请重试'));
      });
  };

  // ── Columns ───────────────────────────────────────────────────────────────

  const columns = useMemo(
    () => [
      {
        title: _l('名称'),
        dataIndex: 'name',
        ellipsis: true,
        render: (text) => (
          <span className="whNameCell">
            <NotificationOutlined className="whIcon" />
            <span>{text}</span>
          </span>
        ),
      },
      {
        title: _l('类型'),
        dataIndex: 'type',
        width: 150,
        render: (type) => {
          const item = WEBHOOK_TYPES.find((t) => t.value === type);
          return (
            <Tag color={WEBHOOK_TYPE_COLOR[type] || 'default'}>
              {item ? item.label : type}
            </Tag>
          );
        },
      },
      {
        title: _l('地址 / 服务器'),
        ellipsis: true,
        render: (_, record) => {
          if (record.type === 'email') {
            return (
              <Tooltip title={`${record.smtpHost}:${record.smtpPort}`}>
                <span className="whUrlCell">
                  <MailOutlined className="whUrlIcon" />
                  {record.smtpHost}:{record.smtpPort}
                </span>
              </Tooltip>
            );
          }
          return (
            <Tooltip title={record.url}>
              <span className="whUrlCell">
                <LinkOutlined className="whUrlIcon" />
                {record.url}
              </span>
            </Tooltip>
          );
        },
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

  // ── Conditional form fields ────────────────────────────────────────────────

  const isEmail = webhookType === 'email';
  const hasAtMention = ['wecom', 'dingtalk', 'feishu'].includes(webhookType);
  const hasMobileAt = ['wecom', 'dingtalk'].includes(webhookType);
  const showSkipSSL = webhookType === 'custom' || webhookType === 'email';

  const isAtAll = Form.useWatch('isAtAll', form);

  // ── Render ────────────────────────────────────────────────────────────────

  return (
    <div className="alertWebhooksContainer">
      {/* ── Toolbar ─────────────────────────────────────────────── */}
      <div className="whToolbar opsPageHeader">
        <div className="opsPageTitle">{_l('通知渠道')}</div>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          {_l('添加渠道')}
        </Button>
      </div>

      {/* ── Table ───────────────────────────────────────────────── */}
      <div className="whTableBox">
        <Table
          loading={loading}
          columns={columns}
          dataSource={webhooks}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false }}
          tableLayout="auto"
          scroll={{ x: 'max-content' }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={_l('暂无通知渠道，点击「添加渠道」新建')}
              />
            ),
          }}
        />
      </div>

      {/* ── Create / Edit Modal ─────────────────────────────────── */}
      <Modal
        title={editingRecord ? _l('编辑通知渠道') : _l('新建通知渠道')}
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
          className="whForm"
          initialValues={emptyForm()}
        >
          {/* 渠道名称 */}
          <Form.Item
            name="name"
            label={_l('渠道名称')}
            rules={[{ required: true, message: _l('请输入渠道名称') }]}
          >
            <Input placeholder={_l('例如：研发群机器人告警')} />
          </Form.Item>

          {/* 渠道类型 */}
          <Form.Item name="type" label={_l('渠道类型')}>
            <Select>
              {WEBHOOK_TYPES.map((t) => (
                <Option key={t.value} value={t.value}>
                  {t.label}
                </Option>
              ))}
            </Select>
          </Form.Item>

          {/* ── 邮件专属字段 ────────────────────────────────────────── */}
          {isEmail && (
            <>
              <Form.Item
                name="smtpHost"
                label={_l('SMTP 服务器')}
                rules={[{ required: true, message: _l('请输入 SMTP 服务器地址') }]}
              >
                <Input placeholder="smtp.exmail.qq.com" />
              </Form.Item>

              <Form.Item
                name="smtpPort"
                label={_l('SMTP 端口')}
                rules={[{ required: true, message: _l('请输入端口号') }]}
              >
                <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="465" />
              </Form.Item>

              <Form.Item
                name="smtpUser"
                label={_l('用户名 / 邮箱')}
                rules={[{ required: true, message: _l('请输入用户名') }]}
              >
                <Input placeholder="alert@company.com" />
              </Form.Item>

              <Form.Item
                name="smtpPass"
                label={_l('密码 / 授权码')}
                extra={editingRecord ? _l('留空表示不修改密码') : undefined}
                rules={
                  editingRecord
                    ? []
                    : [{ required: true, message: _l('请输入密码') }]
                }
              >
                <Input.Password autoComplete="new-password" placeholder="••••••••" />
              </Form.Item>

              <Form.Item
                name="emailTo"
                label={_l('收件人列表')}
                extra={_l('多个收件人用英文逗号分隔')}
                rules={[{ required: true, message: _l('请输入收件人') }]}
              >
                <Input placeholder="it-alert@company.com, dev@company.com" />
              </Form.Item>

              <Form.Item
                name="skipSSL"
                label={_l('忽略 SSL 证书验证')}
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </>
          )}

          {/* ── Webhook URL 字段（非邮件类型）─────────────────────── */}
          {!isEmail && (
            <>
              <Form.Item
                name="url"
                label="Webhook URL"
                rules={[
                  { required: true, message: _l('请输入 Webhook URL') },
                  {
                    pattern: /^https?:\/\/[\w.-]+(:\d+)?(\/.*)?$/i,
                    message: _l('请输入有效的 URL'),
                  },
                ]}
              >
                <Input
                  className="whUrlInput"
                  placeholder={URL_PLACEHOLDER[webhookType] || 'https://'}
                />
              </Form.Item>

              <Form.Item name="proxyUrl" label={_l('正向代理（可选）')}>
                <Input
                  className="whUrlInput"
                  placeholder="http://proxy.internal:8080"
                />
              </Form.Item>

              {/* 跳过 SSL（自定义 Webhook）*/}
              {showSkipSSL && (
                <Form.Item
                  name="skipSSL"
                  label={_l('忽略 SSL 证书验证')}
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
              )}

              {/* 提醒设置（企微 / 钉钉 / 飞书）*/}
              {hasAtMention && (
                <div className="whAtSection">
                  <div className="whAtSectionTitle">{_l('提醒设置')}</div>

                  <Form.Item
                    name="isAtAll"
                    label={_l('@所有人')}
                    valuePropName="checked"
                  >
                    <Switch />
                  </Form.Item>

                  {!isAtAll && (
                    <>
                      {hasMobileAt && (
                        <Form.Item
                          name="mentionedMobileList"
                          label={_l('手机号 @（逗号分隔）')}
                        >
                          <Input placeholder="13800138000, 18612345678" />
                        </Form.Item>
                      )}

                      <Form.Item
                        name="mentionedList"
                        label={_l('用户 ID @（逗号分隔）')}
                      >
                        <Input placeholder="user1, user2" />
                      </Form.Item>
                    </>
                  )}
                </div>
              )}
            </>
          )}
        </Form>
      </Modal>
    </div>
  );
};

export default Webhooks;
