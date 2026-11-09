import React from 'react';
import { BrowserRouter as Router } from 'react-router';
import { ConfigProvider } from 'antd';

import RoutesComponent from './routes/RoutesComponent';
import { BASE_PATH } from './config';

const fontFamily = "'Microsoft YaHei', Arial, sans-serif";

const App = () => {
  return (
    <ConfigProvider
      theme={{
        token: {
          colorPrimary: '#146ef5',
          colorText: '#273142',
          colorTextHeading: '#111827',
          colorBorder: '#d6dee8',
          colorBgLayout: '#f4f7fb',
          colorBgContainer: '#ffffff',
          borderRadius: 6,
          controlHeight: 34,
          fontFamily,
          fontFamilyCode: fontFamily,
        },
        components: {
          Button: {
            borderRadius: 6,
            controlHeight: 34,
            controlHeightSM: 28,
            controlHeightLG: 40,
            paddingInline: 12,
            paddingInlineSM: 8,
            paddingInlineLG: 16,
            primaryShadow: 'none',
            defaultShadow: 'none',
            dangerShadow: 'none',
          },
          Modal: {
            borderRadiusLG: 10,
            titleFontSize: 16,
            titleLineHeight: 1.5,
          },
          Popover: {
            borderRadiusLG: 8,
          },
          Dropdown: {
            borderRadiusLG: 8,
          },
          Input: {
            activeShadow: '0 0 0 3px rgba(20, 110, 245, 0.10)',
          },
          Select: {
            activeBorderColor: '#146ef5',
            activeOutlineColor: 'rgba(20, 110, 245, 0.10)',
          },
          Table: {
            cellFontSize: 13,
            cellPaddingBlock: 12,
            cellPaddingInline: 12,
            headerBg: '#f7f9fc',
            headerColor: '#111827',
            headerSplitColor: '#e7edf4',
            borderColor: '#e7edf4',
            rowHoverBg: '#f2f7ff',
          },
          Menu: {
            itemBorderRadius: 6,
            itemSelectedBg: '#edf5ff',
            itemSelectedColor: '#146ef5',
            itemHoverBg: '#f7f9fc',
            itemHoverColor: '#111827',
          },
        },
      }}
    >
      <Router basename={BASE_PATH}>
        <RoutesComponent />
      </Router>
    </ConfigProvider>
  );
};

export default App;
