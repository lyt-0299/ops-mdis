import _ from 'lodash';
import moment from 'moment';

import langConfig from './langConfig';

export const isUrl = str => {
  if (
    /^http(s)?:\/\/(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])\.(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])\.(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])\.(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])$/.test(
      str,
    )
  ) {
    return false;
  }
  var domainNamePatrn =
    /^http(s)?:\/\/[a-zA-Z0-9][-a-zA-Z0-9]{0,62}(\.[a-zA-Z0-9][-a-zA-Z0-9]{0,62})+(:([0-9]|[1-9]\d{1,3}|[1-5]\d{4}|6[0-5]{2}[0-3][0-5]))?$/;
  var IPPatrn =
    /^http(s)?:\/\/(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])\.(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])\.(\d{1,2}|1\d\d|2[0-4]\d|25[0-5])\.(\d{1,2}|1\d\d|2[0-4]\d|25[0-5]):([0-9]|[1-9]\d{1,3}|[1-5]\d{4}|6[0-5]{2}[0-3][0-5])$/;
  return IPPatrn.test(str) || domainNamePatrn.test(str);
};

export const formatFileSize = (size, accuracy, space, units) => {
  units = units || ['B', 'KB', 'MB', 'GB', 'TB'];
  space = space || ' ';
  accuracy = (accuracy && typeof accuracy === 'number' && accuracy) || 0;
  if (!size) {
    return '0' + space + units[0];
  }
  var i = Math.floor(Math.log(size) / Math.log(1024));
  return (size / Math.pow(1024, i)).toFixed(accuracy) * 1 + space + units[i];
};

export const urlToObj = url => {
  var reg_url = /^[^?]+\?([\w\W]+)$/,
    reg_para = /([^&=]+)=([\w\W]*?)(&|$|#)/g,
    arr_url = reg_url.exec(url),
    ret = {};
  if (arr_url && arr_url[1]) {
    var str_para = arr_url[1],
      result;
    while ((result = reg_para.exec(str_para)) != null) {
      ret[result[1]] = result[2];
    }
  }
  return ret;
};

const htmlEncodeReg = str => {
  const encodeHTMLRules = {
    '&': '&#38;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&#34;',
    "'": '&#39;',
    '/': '&#47;',
  };
  const matchHTML = /&(?!#?\w+;)|<|>|"|'|\//g;
  return str
    ? str.toString().replace(matchHTML, function (m) {
        return encodeHTMLRules[m] || m;
      })
    : '';
};

const htmlDecodeReg = str => {
  const decodeHTMLRules = {
    '&#38;': '&',
    '&amp;': '&',
    '&#60;': '<',
    '&#62;': '>',
    '&#34;': '"',
    '&#39;': "'",
    '&#47;': '/',
    '&lt;': '<',
    '&gt;': '>',
    '&quot;': '"',
  };
  const matchHTML = /&#(38|60|62|34|39|47);|&(amp|lt|gt|quot);/g;
  return str
    ? str.toString().replace(matchHTML, function (m) {
        return decodeHTMLRules[m] || m;
      })
    : '';
};

export const highlightMessageText = (keyword, content) => {
  content = htmlDecodeReg(content);
  const reg = new RegExp(_.escapeRegExp(keyword), 'gi');
  const exec = reg.exec(content);
  const newKeyword = exec ? exec[0] : '';
  content = htmlEncodeReg(content.replace(new RegExp(newKeyword, 'g'), '*#span1#*' + newKeyword + '*#span2#*'));
  content = content.replace(/\*#span1#\*/g, '<span class="highlig">').replace(/\*#span2#\*/g, '</span>');
  return content;
};

export const setLocalStorage = (name, data, expire = new Date().getTime() + 6 * 24 * 60 * 60 * 1000) => {
  const obj = {
    data,
    expire,
  };
  localStorage.setItem(name, JSON.stringify(obj));
};

export const getLocalStorage = name => {
  const storage = localStorage.getItem(name);
  const time = new Date().getTime();

  let result = undefined;
  if (storage) {
    console.log(storage);
    const obj = JSON.parse(storage);
    if (time < obj.expire) {
      result = obj.data;
    } else {
      localStorage.removeItem(name);
    }
  }
  return result;
};

export const _l = function (key, ...args) {
  let content = key;
  let { translations } = window;

  // 翻译文件内存在这个key
  if (typeof translations !== 'undefined' && translations[key]) {
    content = translations[key];
  }

  // 含有0%、1%等内容参数替换
  if (args.length > 0) {
    for (let i = 0; i < args.length; i++) {
      content = content.replace(new RegExp(`%${i}`, 'g'), args[i]);
    }
  } else if (/.*%\d{5}/.test(content)) {
    // 处理特殊多语境单词问题
    content = content.replace(/%\d{5}$/, '');
  }

  return content;
};

window._l = _l;

window.getCookie = function getCookie(name) {
  if (_.get(window, 'md.global.Config.HttpOnly')) {
    return localStorage.getItem(name) || null;
  }

  const cookieRegex = new RegExp(`(^| )${name}=([^;]*)(;|$)`);
  const cookieMatch = document.cookie.match(cookieRegex);

  return cookieMatch ? decodeURIComponent(cookieMatch[2]) : null;
};

window.setCookie = function setCookie(name, value, expire) {
  // 过期时间处理
  const expiration = expire ? moment(expire).toDate() : moment().add(10, 'days').toDate();

  const cookieOptions = {
    expires: expiration.toGMTString(),
    path: '/',
    domain: '',
  };

  document.cookie = `${name}=${escape(value)};expires=${
    cookieOptions.expires
  };path=${cookieOptions.path};domain=${cookieOptions.domain}`;
};

export const initLang = () => {
  const defaultLang = navigator.language === 'zh-CN' ? 'zh_Hans' : 'en';
  const lang = window.getCookie('i18n_langtag') || defaultLang;
  const currentLang = langConfig.find(item => item.key === lang);

  if (!!currentLang) {
    let xhrObj = new XMLHttpRequest();
    xhrObj.open('GET', currentLang.path, false);
    xhrObj.send('');
    let script = document.createElement('script');
    script.type = 'text/javascript';
    script.text = xhrObj.responseText;
    document.head.appendChild(script);
  }
};

// 剥掉 basename（从 <base> 标签读取）后的路由段
// 例：basename=/mdis/web, /mdis/web/mongodb/slowQuery → ['mongodb','slowQuery']
export const getRouteSegments = () => {
  const base = (document.querySelector('base')?.getAttribute('href') || '/web/').replace(/\/$/, '');
  const path = window.location.pathname;
  const routePath = path.startsWith(base) ? path.slice(base.length) : path;
  return routePath.split('/').filter(Boolean);
};

export const getRouteKey = () => getRouteSegments()[0] || '';

/**
 * iframe 页面在 iframeUrls 里的键。多数页面就是第一段路由；
 * 自定义页面例外——它们的键带 slug（custom/langfuse），因为一个 /custom 路由下有任意多个页面。
 */
export const getIframeKey = () => {
  const segments = getRouteSegments();
  if (segments[0] === 'custom' && segments[1]) return `custom/${segments[1]}`;
  return segments[0] || '';
};

export const formatNumber = num => {
  return num.toLocaleString('en-US');
};

export const formatTime = milliseconds => {
  const duration = moment.duration(milliseconds);
  const minutes = Math.floor(duration.asMinutes());
  const hours = duration.asHours();
  const days = duration.asDays();

  if (minutes < 60) {
    return `${minutes} ${_l('分钟')}`;
  } else if (hours < 24) {
    return Number.isInteger(hours) ? `${hours} ${_l('小时')}` : `${hours.toFixed(1)} ${_l('小时')}`;
  } else {
    return Number.isInteger(days) ? `${days} ${_l('天')}` : `${days.toFixed(1)} ${_l('天')}`;
  }
};
