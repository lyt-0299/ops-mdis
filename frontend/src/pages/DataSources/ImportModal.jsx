/**
 * ImportModal — 数据源批量导入
 *
 * 1.5.0 起数据源只有这一个入口（ops.yaml 的 ENV_* 已废弃），批量接入的需求由本弹窗承接。
 *
 * 设计要点：**导入即验证**。批量导入相对以前写 ENV_* 的价值不在「换个地方填」，
 * 而在填错时当场知道是哪一条、错在哪——以前的做法是配错了照样写进去，
 * UI 看着正常、采集器不报错，等发现没数据再一层层往回刨。
 *
 * 所以这里：
 *   - 先「仅校验」（dryRun）：只做格式校验 + 逐条探测连接，不写库
 *   - 结果逐条列出，连不通的标出来，但**不阻止导入**（配置可以先落地、服务稍后再起，
 *     与手工保存的行为保持一致：此刻通不通不该否决配置，但必须让用户知情）
 */
import React, { useCallback, useMemo, useState } from 'react';
import { InboxOutlined } from '@ant-design/icons';
import { Alert, Button, Input, message, Modal, Space, Table, Tag, Tooltip, Typography, Upload } from 'antd';

import { importSources, importTemplateUrl } from '@/utils/alertApi';
import { apiErr } from '@/utils/apiError';
import { _l } from '@/utils/utils';

const { Dragger } = Upload;
const { Paragraph, Text } = Typography;

// 单文件上限，与后端 importMaxBytes 对齐（2 MiB）。
const MAX_BYTES = 2 * 1024 * 1024;

/** 结果行的动作标签。只用中性灰 / 绿 / 红三色，不引入新颜色。 */
const ActionTag = ({ action }) => {
  if (action === 'created') return <Tag color="success">{_l('新增')}</Tag>;
  if (action === 'updated') return <Tag color="success">{_l('更新')}</Tag>;
  return <Tag color="error">{_l('失败')}</Tag>;
};

const ImportModal = ({ open, onClose, onImported }) => {
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState(null);

  const reset = useCallback(() => {
    setText('');
    setFileName('');
    setResult(null);
    setSubmitting(false);
  }, []);

  const handleClose = useCallback(() => {
    reset();
    onClose();
  }, [onClose, reset]);

  // 读文件内容到内存，不走真正的上传请求（导入是一次 POST，没必要两趟）。
  const beforeUpload = useCallback((file) => {
    if (file.size > MAX_BYTES) {
      message.error(_l('文件超过 2 MB'));
      return Upload.LIST_IGNORE;
    }
    const reader = new FileReader();
    reader.onload = () => {
      setText(String(reader.result || ''));
      setFileName(file.name);
      setResult(null);
    };
    reader.onerror = () => message.error(_l('读取文件失败'));
    reader.readAsText(file);
    return Upload.LIST_IGNORE; // 自己接管，不进 antd 的文件列表
  }, []);

  const submit = useCallback(
    (dryRun) => {
      if (!text.trim()) {
        message.warning(_l('请先选择或粘贴导入文件'));
        return;
      }
      setSubmitting(true);
      importSources(text, { dryRun })
        .then(({ data }) => {
          setResult(data);
          if (!dryRun && data && data.failed === 0) {
            message.success(_l('导入完成'));
          }
          // 真正写入过就刷新列表，哪怕有部分失败——成功的那些已经落库了。
          if (!dryRun && data && data.created + data.updated > 0) onImported();
        })
        .catch((e) => message.error(apiErr(e, _l('导入失败'))))
        .finally(() => setSubmitting(false));
    },
    [text, onImported],
  );

  const columns = useMemo(
    () => [
      {
        title: _l('名称'),
        dataIndex: 'name',
        width: 180,
        ellipsis: true,
        render: (v, r) => v || <Text type="secondary">{_l('第 %0 条', r.index + 1)}</Text>,
      },
      { title: _l('类型'), dataIndex: 'kind', width: 140, ellipsis: true },
      {
        title: _l('结果'),
        dataIndex: 'action',
        width: 90,
        render: (v) => <ActionTag action={v} />,
      },
      {
        title: _l('连接'),
        width: 110,
        render: (_v, r) => {
          if (r.action === 'failed' && !r.connectError) return <Text type="secondary">—</Text>;
          if (r.connectOK) return <Text type="success">{_l('可连接')}</Text>;
          return (
            <Tooltip title={r.connectError}>
              <Text type="danger">{_l('连不上')}</Text>
            </Tooltip>
          );
        },
      },
      {
        title: _l('说明'),
        ellipsis: true,
        render: (_v, r) => {
          if (r.action === 'failed') {
            // errorKey 是后端英文机器键，交给统一映射出中文
            const msg = apiErr({ response: { data: { key: r.errorKey, detail: r.detail } } }, _l('校验未通过'));
            return <Text type="danger">{msg}</Text>;
          }
          if (r.connectError) return <Text type="secondary">{r.connectError}</Text>;
          return <Text type="secondary">—</Text>;
        },
      },
    ],
    [],
  );

  const summary = useMemo(() => {
    if (!result) return null;
    const dry = result.dryRun;
    const okCount = result.created + result.updated;
    const unreachable = (result.results || []).filter((r) => r.action !== 'failed' && !r.connectOK).length;

    const parts = [
      dry
        ? _l('预检完成：%0 条可导入，%1 条未通过校验', okCount, result.failed)
        : _l('导入完成：新增 %0，更新 %1，失败 %2', result.created, result.updated, result.failed),
    ];
    if (unreachable > 0) {
      parts.push(
        dry
          ? _l('其中 %0 条当前连不上（不影响导入，可先落配置）', unreachable)
          : _l('其中 %0 条当前连不上——配置已保存，等服务起来后会自动开始采集', unreachable),
      );
    }
    return {
      type: result.failed > 0 ? 'warning' : 'success',
      text: parts.join('；'),
      dry,
    };
  }, [result]);

  return (
    <Modal
      open={open}
      title={_l('批量导入数据源')}
      width={860}
      onCancel={handleClose}
      maskClosable={false}
      footer={
        <Space>
          <Button onClick={handleClose}>{_l('关闭')}</Button>
          <Button loading={submitting} disabled={!text.trim()} onClick={() => submit(true)}>
            {_l('仅校验')}
          </Button>
          <Button type="primary" loading={submitting} disabled={!text.trim()} onClick={() => submit(false)}>
            {_l('导入')}
          </Button>
        </Space>
      }
    >
      <Paragraph type="secondary" style={{ marginBottom: 12 }}>
        {_l('支持 YAML 或 JSON。按「类型 + 名称」幂等：同名条目是更新而不是新建，同一份文件可以反复导入。')}
        <br />
        {_l('建议先跑一遍「仅校验」——只做格式校验和连接探测，不写入任何数据。')}
        <br />
        <a href={importTemplateUrl()} download>
          {_l('下载 YAML 模板')}
        </a>
        {_l('（含各类型示例与填写说明）')}
      </Paragraph>

      <Dragger
        accept=".yaml,.yml,.json,.txt"
        multiple={false}
        beforeUpload={beforeUpload}
        showUploadList={false}
        style={{ marginBottom: 12 }}
      >
        <p className="ant-upload-drag-icon" style={{ marginBottom: 4 }}>
          <InboxOutlined />
        </p>
        <p className="ant-upload-text">
          {fileName ? _l('已选择：%0', fileName) : _l('点击或拖拽导入文件到此处')}
        </p>
        <p className="ant-upload-hint">{_l('支持 .yaml / .yml / .json，单文件不超过 2 MB')}</p>
      </Dragger>

      <Input.TextArea
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          setFileName('');
          setResult(null);
        }}
        rows={8}
        spellCheck={false}
        placeholder={_l('也可以直接把内容粘贴到这里')}
        style={{ fontFamily: 'monospace', fontSize: 12 }}
      />

      {summary && (
        <>
          <Alert
            type={summary.type}
            showIcon
            style={{ marginTop: 12, marginBottom: 8 }}
            message={summary.text}
            description={summary.dry ? _l('以上为预检结果，尚未写入。确认无误后点「导入」。') : undefined}
          />
          <Table
            size="small"
            rowKey="index"
            columns={columns}
            dataSource={result.results || []}
            pagination={false}
            scroll={{ y: 260 }}
          />
        </>
      )}
    </Modal>
  );
};

export default ImportModal;
