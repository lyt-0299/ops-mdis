import React, { useEffect, useRef,useState } from 'react';
import { useSelector } from 'react-redux';
import { useLocation } from 'react-router';
import { DownloadOutlined } from '@ant-design/icons';
import { Button, message } from 'antd';

import { API_BASE_PATH } from '@/config';
import { apiErr } from '@/utils/apiError';
import { _l,getIframeKey } from '@/utils/utils';

import './index.less';

// 日志页路由 key → 导出类型(其余 iframe 页无导出)
const LOG_EXPORT_TYPE = {
  serviceLogs: 'service',
  containerConsole: 'container',
};

const Iframe = () => {
  const { pathname } = useLocation();
  const iframeUrls = useSelector((state) => state.iframe.iframeUrls);
  const [iframeKey, setIframeKey] = useState('');
  const [frameError, setFrameError] = useState(null);
  const [frameReloadKey, setFrameReloadKey] = useState(0);
  const [exportButtonPos, setExportButtonPos] = useState(null);
  const iframeRef = useRef(null);
  const pageRef = useRef(null);
  const dragRef = useRef(null);
  const suppressClickRef = useRef(false);

  useEffect(() => {
    setIframeKey(getIframeKey());
    setFrameError(null);
  }, [pathname]);

  const exportType = LOG_EXPORT_TYPE[iframeKey];

  useEffect(() => {
    if (!exportType) {
      setExportButtonPos(null);
      return;
    }
    try {
      const raw = localStorage.getItem(`mdis-log-export-pos-${exportType}`);
      setExportButtonPos(raw ? JSON.parse(raw) : null);
    } catch (e) {
      setExportButtonPos(null);
    }
  }, [exportType]);

  useEffect(() => {
    const handleMove = (event) => {
      if (!dragRef.current || !pageRef.current) return;
      const rect = pageRef.current.getBoundingClientRect();
      const { startX, startY, originX, originY } = dragRef.current;
      const x = Math.min(
        Math.max(12, originX + event.clientX - startX),
        Math.max(12, rect.width - 150),
      );
      const y = Math.min(
        Math.max(12, originY + event.clientY - startY),
        Math.max(12, rect.height - 52),
      );
      dragRef.current.moved = true;
      setExportButtonPos({ x, y });
    };

    const handleUp = () => {
      if (!dragRef.current) return;
      if (dragRef.current.moved) {
        suppressClickRef.current = true;
        setTimeout(() => {
          suppressClickRef.current = false;
        }, 0);
      }
      if (dragRef.current.exportType && exportButtonPos) {
        localStorage.setItem(
          `mdis-log-export-pos-${dragRef.current.exportType}`,
          JSON.stringify(exportButtonPos),
        );
      }
      dragRef.current = null;
    };

    window.addEventListener('pointermove', handleMove);
    window.addEventListener('pointerup', handleUp);
    return () => {
      window.removeEventListener('pointermove', handleMove);
      window.removeEventListener('pointerup', handleUp);
    };
  }, [exportButtonPos]);

  // 读取嵌入 Grafana 的当前筛选(同源 iframe)，透传给后端导出，保证导出与页面所见一致
  // (时间范围/服务/级别/容器/关键字 都跟随页面当前所选)。
  const handleExport = async () => {
    const params = new URLSearchParams();
    params.set('type', exportType);
    params.set('format', 'txt');
    // inline=1：后端返回纯文本数据(不带 attachment 下载头)，前端 fetch 取回后本地生成 blob
    // 下载。这样走的是 XHR 取数而非浏览器“下载”，能绕开企业安全网关/DLP 代理把直链下载
    // 的文件名改写成随机 UUID 的问题；文件名由下方 a.download 决定(blob 同源，属性生效)。
    params.set('inline', '1');
    try {
      const gp = new URLSearchParams(iframeRef.current?.contentWindow?.location?.search || '');
      const pass = (gvar, key) => {
        const vals = gp.getAll(gvar).filter(Boolean);
        if (vals.length) params.set(key, vals.join(','));
      };
      if (exportType === 'service') {
        pass('var-service_name', 'service_name');
        pass('var-detected_level', 'detected_level');
      } else {
        pass('var-container', 'container');
      }
      const search = gp.get('var-search');
      if (search) params.set('search', search);
      if (gp.get('from')) params.set('from', gp.get('from'));
      if (gp.get('to')) params.set('to', gp.get('to'));
    } catch (e) {
      // 跨域/未就绪 → 用默认(全部 + 近 14 天)
    }
    const p2 = (n) => String(n).padStart(2, '0');
    const d = new Date();
    const stamp = `${d.getFullYear()}${p2(d.getMonth() + 1)}${p2(d.getDate())}-${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`;
    const filename = `${exportType}-logs-${stamp}.txt`;
    try {
      const resp = await fetch(`${API_BASE_PATH}/log/export?${params.toString()}`, { credentials: 'include' });
      if (!resp.ok) {
        let data = null;
        try { data = await resp.json(); } catch (e) { /* 非 JSON 错误体 */ }
        message.error(apiErr({ response: { data } }, _l('导出日志失败')));
        return;
      }
      const blob = await resp.blob();
      const objUrl = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = objUrl;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(() => URL.revokeObjectURL(objUrl), 1000);
      message.success(_l('已开始下载'));
    } catch (e) {
      message.error(apiErr(e, _l('导出日志失败')));
    }
  };

  const handleExportPointerDown = (event) => {
    if (!pageRef.current) return;
    const pageRect = pageRef.current.getBoundingClientRect();
    const buttonRect = event.currentTarget.getBoundingClientRect();
    dragRef.current = {
      exportType,
      startX: event.clientX,
      startY: event.clientY,
      originX: buttonRect.left - pageRect.left,
      originY: buttonRect.top - pageRect.top,
      moved: false,
    };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };

  const handleExportClick = (event) => {
    if (suppressClickRef.current) {
      event.preventDefault();
      return;
    }
    handleExport();
  };

  const handleIframeLoad = () => {
    setFrameError(null);
    try {
      const doc = iframeRef.current?.contentDocument || iframeRef.current?.contentWindow?.document;
      const text = doc?.body?.innerText || '';
      if (/502\s+Bad\s+Gateway/i.test(text)) {
        setFrameError('502');
      }
    } catch (e) {
      // Cross-origin pages cannot be inspected; keep the iframe visible.
    }
  };

  const reloadIframe = () => {
    setFrameError(null);
    setFrameReloadKey(key => key + 1);
  };

  if (!iframeUrls[iframeKey]?.length) return null;

  return (
    <div className="iframePage" ref={pageRef}>
      {exportType && (
        <Button
          type="primary"
          icon={<DownloadOutlined />}
          className="logExportFloat"
          style={exportButtonPos ? { left: exportButtonPos.x, top: exportButtonPos.y, right: 'auto', bottom: 'auto' } : undefined}
          onPointerDown={handleExportPointerDown}
          onClick={handleExportClick}
        >
          {_l('导出日志')}
        </Button>
      )}
      {frameError === '502' && (
        <div className="iframeFriendlyError">
          <div className="iframeFriendlyError__card">
            <div className="iframeFriendlyError__code">502</div>
            <div className="iframeFriendlyError__title">{_l('页面暂时不可用')}</div>
            <div className="iframeFriendlyError__desc">
              {_l('嵌入页面返回 502，可能是对应服务未启动或网关暂时无法连接。')}
            </div>
            <button type="button" className="iframeFriendlyError__btn" onClick={reloadIframe}>
              {_l('重试加载')}
            </button>
          </div>
        </div>
      )}
      <iframe
        key={`${iframeKey}-${frameReloadKey}`}
        ref={iframeRef}
        title={iframeKey}
        className="iframePageFrame"
        src={iframeUrls[iframeKey]}
        onLoad={handleIframeLoad}
        sandbox="allow-forms allow-scripts allow-same-origin"
      />
    </div>
  );
};

export default React.memo(Iframe);
