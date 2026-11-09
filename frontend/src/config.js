// 从 <base> 标签读取运行时路径（entrypoint.sh 注入），去掉结尾的 /
const baseHref = (document.querySelector('base')?.getAttribute('href') || '/web/').replace(/\/$/, '');

// baseHref 形如 /mdis/web 或 /web
const BASE_PATH = baseHref;
const API_BASE_PATH = baseHref.replace(/\/web$/, '') + '/api';

export { API_BASE_PATH,BASE_PATH };
