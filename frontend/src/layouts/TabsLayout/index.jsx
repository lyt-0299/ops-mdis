import React, { Fragment } from 'react';
import { Outlet } from 'react-router';

import Tabs from '../components/Tabs';

const TabsLayout = () => {
  return (
    <Fragment>
      <Tabs></Tabs>
      <div className="flex">
        <Outlet></Outlet>
      </div>
    </Fragment>
  );
}

export default TabsLayout;
