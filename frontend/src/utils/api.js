import { message } from 'antd';
import axios from 'axios';

import { API_BASE_PATH,BASE_PATH } from '../config';

import { _l } from './utils';

const axiosInstance = axios.create({
  baseURL: API_BASE_PATH,
  timeout: 20000,
  headers: {},
});

axiosInstance.interceptors.request.use(
  (config) => {
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

axiosInstance.interceptors.response.use(
  (response) => {
    if (response.status === 200) return response;
    return Promise.reject(response);
  },
  (error) => {
    switch (error.status) {
      case 401:
        // message.error(_l('Token认证失败'));
        const authPath = `${BASE_PATH}/auth`;
        if (authPath !== window.location.pathname) {
          // 立即跳转(不延迟),避免未登录时先渲染主框架再跳登录的空白闪烁;
          // replace 不在历史留未登录 URL
          window.location.replace(authPath);
        }
        break;
      default:
    }
    return Promise.reject(error);
  }
);

const get = (url, params = {}, config = {}) => {
  return axiosInstance.get(url, { params, ...config });
};

const post = (url, data = {}, config = {}) => {
  return axiosInstance.post(url, data, { ...config });
};

/**
 * 认证接口
 * @param {String} token 认证
 */
export const getAuth = (token) =>
  post(
    '/auth',
    {},
    {
      headers: {
        Authorization: token,
      },
    }
  );

export const logout = () => post('/logout');

/**
 * 获取 iframe URL 列表
 */
export const getIframeUrls = () => get('/iframe-urls');

/**
 * 获取历史慢查询列表
 */
export const getSlowQueryList = (args) => post('/slow-query/list', args);

/**
 * 创建索引
 * @param {Object} args 请求参数
 * @param {String} args.dbname 数据库名称
 * @param {String} args.worksheetID 工作表ID
 * @param {String} args.controlID 控件ID
 */
export const createIndex = (args) => post('/index/create', args);

/**
 * 立即执行慢查询结果分析
 */
export const executeTask = () => post('/slow-query/analyze', {});

/**
 * 获取数据库慢查询记录开关是否开启
 */
export const profilingStatus = (args) => post('/slow-query/profiling-status', args);

export const getSlowQueryDetail = () => get('/global-args');
