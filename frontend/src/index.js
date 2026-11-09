import React from 'react';
import { createRoot } from 'react-dom/client';
import { Provider } from 'react-redux';

import { initLang } from './utils/utils';
import App from './App';
import store from './store';

import './styles/basic.less';

initLang();
document.title = _l('运维平台');

createRoot(document.getElementById('root')).render(
  <Provider store={store}>
    <App />
  </Provider>
);
