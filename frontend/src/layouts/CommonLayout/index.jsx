import React from 'react';

import Header from '../components/Header';

import './index.less';

const NoSidebarLayout = ({ children }) => {
  return (
    <div className="commonLayout">
      <Header />
      <div className="commonContentContainer">{children}</div>
    </div>
  );
};

export default NoSidebarLayout;