import React from 'react';
import { Outlet,useRoutes } from 'react-router';

import CommonLayout from '../layouts/CommonLayout';
import SidebarLayout from '../layouts/SidebarLayout';

import { routes } from './index';

const RoutesComponent = () => {
  const processRoutes = (routes) =>
    routes.map(({ path, element, layout, children }) => {
      if (layout === 'none') {
        return {
          path,
          element,
          children,
        };
      }
      const Layout = layout === 'common' ? CommonLayout : SidebarLayout;
      return {
        path,
        element: (
          <Layout>
            {element}
          </Layout>
        ),
        children,
      };
    });

    return useRoutes(processRoutes(routes));
};

export default RoutesComponent;
