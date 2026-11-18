import { lazy } from 'react';
import type { ComponentType } from 'react';
import {
  ApiOutlined,
  ReadOutlined,
  ClusterOutlined,
  CloudServerOutlined,
  CrownOutlined,
  DashboardOutlined,
  ExportOutlined,
  GlobalOutlined,
  ImportOutlined,
  SettingOutlined,
  SwapOutlined,
  TagsOutlined,
  TeamOutlined,
  ToolOutlined,
} from '@ant-design/icons';

const AccessGuidePage = lazy(() => import('@/pages/access-guide/AccessGuidePage'));
const IndexPage = lazy(() => import('@/pages/index/IndexPage'));
const InboundsPage = lazy(() => import('@/pages/inbounds/InboundsPage'));
const ClientsPage = lazy(() => import('@/pages/clients/ClientsPage'));
const GroupsPage = lazy(() => import('@/pages/groups/GroupsPage'));
const NodesPage = lazy(() => import('@/pages/nodes/NodesPage'));
const HostsPage = lazy(() => import('@/pages/hosts/HostsPage'));
const ServiceHostsPage = lazy(() => import('@/pages/service-hosts/ServiceHostsPage'));
const CommercePage = lazy(() => import('@/pages/commerce/CommercePage'));
const SettingsPage = lazy(() => import('@/pages/settings/SettingsPage'));
const XrayPage = lazy(() => import('@/pages/xray/XrayPage'));
const ApiDocsPage = lazy(() => import('@/pages/api-docs/ApiDocsPage'));
const SponsorsPage = lazy(() => import('@/pages/sponsors/SponsorsPage'));

export interface PanelRoute {
  group: 'access' | 'management';
  path: string;
  titleKey: string;
  icon: ComponentType;
  component: ComponentType;
}

// The router, the sidebar, the page titles and the command palette all read this list, so a
// page cannot go missing from one of them.
export const PANEL_ROUTES: PanelRoute[] = [
  {
    group: 'access',
    path: '/access-guide',
    titleKey: 'menu.accessGuide',
    icon: ReadOutlined,
    component: AccessGuidePage,
  },
  {
    group: 'access',
    path: '/clients',
    titleKey: 'menu.clients',
    icon: TeamOutlined,
    component: ClientsPage,
  },
  {
    group: 'access',
    path: '/hosts',
    titleKey: 'menu.hosts',
    icon: GlobalOutlined,
    component: HostsPage,
  },
  {
    group: 'access',
    path: '/nodes',
    titleKey: 'menu.nodes',
    icon: ClusterOutlined,
    component: NodesPage,
  },
  {
    group: 'access',
    path: '/inbounds',
    titleKey: 'menu.inbounds',
    icon: ImportOutlined,
    component: InboundsPage,
  },
  {
    group: 'access',
    path: '/routing',
    titleKey: 'menu.routing',
    icon: SwapOutlined,
    component: XrayPage,
  },
  {
    group: 'access',
    path: '/outbound',
    titleKey: 'menu.outbounds',
    icon: ExportOutlined,
    component: XrayPage,
  },
  {
    group: 'management',
    path: '/',
    titleKey: 'menu.dashboard',
    icon: DashboardOutlined,
    component: IndexPage,
  },
  {
    group: 'management',
    path: '/service-hosts',
    titleKey: 'menu.serviceHosts',
    icon: CloudServerOutlined,
    component: ServiceHostsPage,
  },
  {
    group: 'management',
    path: '/groups',
    titleKey: 'menu.groups',
    icon: TagsOutlined,
    component: GroupsPage,
  },
  {
    group: 'management',
    path: '/commerce',
    titleKey: 'menu.commerce',
    icon: TagsOutlined,
    component: CommercePage,
  },
  {
    group: 'management',
    path: '/settings',
    titleKey: 'menu.settings',
    icon: SettingOutlined,
    component: SettingsPage,
  },
  {
    group: 'management',
    path: '/xray',
    titleKey: 'menu.xray',
    icon: ToolOutlined,
    component: XrayPage,
  },
  {
    group: 'management',
    path: '/api-docs',
    titleKey: 'menu.apiDocs',
    icon: ApiOutlined,
    component: ApiDocsPage,
  },
  {
    group: 'management',
    path: '/sponsors',
    titleKey: 'menu.sponsors',
    icon: CrownOutlined,
    component: SponsorsPage,
  },
];

export const PANEL_ROUTE_PATHS = PANEL_ROUTES.map((route) => route.path);

export const PANEL_TITLE_KEYS: Record<string, string> = Object.fromEntries(
  PANEL_ROUTES.map((route) => [route.path, route.titleKey]),
);
