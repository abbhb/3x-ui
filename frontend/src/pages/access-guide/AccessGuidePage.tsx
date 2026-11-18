import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, ConfigProvider, Layout, Tabs } from 'antd';
import {
  ArrowRightOutlined,
  ClusterOutlined,
  ExportOutlined,
  GlobalOutlined,
  ImportOutlined,
  SwapOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import { useTheme } from '@/hooks/useTheme';
import AppSidebar from '@/layouts/AppSidebar';
import english from './en-US.json';
import simplifiedChinese from './zh-CN.json';
import traditionalChinese from './zh-TW.json';
import './AccessGuidePage.css';

const flow = [
  { key: 'clients', icon: TeamOutlined },
  { key: 'hosts', icon: GlobalOutlined },
  { key: 'nodes', icon: ClusterOutlined },
  { key: 'inbounds', icon: ImportOutlined },
  { key: 'routing', icon: SwapOutlined },
  { key: 'outbounds', icon: ExportOutlined },
] as const;

const steps = {
  node: '/nodes',
  inbound: '/inbounds',
  forward: '/routing',
  host: '/hosts',
  enable: '/settings#subscription',
  client: '/clients',
  plan: '/commerce?tab=plans',
  account: '/commerce?tab=accounts',
  portal: '/commerce',
  deliver: '/clients',
  reuse: '/nodes',
  listener: '/inbounds',
  alias: '/hosts',
  attach: '/clients',
};

const scenarios = {
  first: ['node', 'inbound', 'forward', 'host', 'enable', 'client', 'deliver'],
  extend: ['reuse', 'listener', 'alias', 'attach', 'deliver'],
  issue: ['enable', 'client', 'deliver'],
  portal: ['plan', 'account', 'portal'],
} as const;

export default function AccessGuidePage() {
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage ?? i18n.language;
  const copy =
    language === 'zh-CN' ? simplifiedChinese : language === 'zh-TW' ? traditionalChinese : english;
  const { antdThemeConfig } = useTheme();
  return (
    <ConfigProvider theme={antdThemeConfig}>
      <Layout hasSider className="access-guide-layout">
        <AppSidebar />
        <Layout.Content className="access-guide">
          <h1>{t('menu.accessGuide')}</h1>
          <figure className="guide-flow">
            <figcaption>{copy.flow}</figcaption>
            <ol>
              {flow.map(({ key, icon: Icon }) => (
                <li key={key}>
                  <Icon aria-hidden="true" />
                  <strong>{t(`menu.${key}`)}</strong>
                  <small>{copy.flowItems[key]}</small>
                </li>
              ))}
            </ol>
          </figure>
          <Card>
            <Tabs
              items={(Object.keys(scenarios) as (keyof typeof scenarios)[]).map((key) => ({
                key,
                label: copy.scenarios[key],
                children: (
                  <ol className="guide-steps">
                    {scenarios[key].map((step) => (
                      <li key={step}>
                        <h2>{copy.steps[step].title}</h2>
                        <p>{copy.steps[step].body}</p>
                        <Link to={steps[step]}>
                          {copy.steps[step].link} <ArrowRightOutlined aria-hidden />
                        </Link>
                        {step === 'forward' && (
                          <Link to="/outbound">
                            {t('menu.outbounds')} <ArrowRightOutlined aria-hidden />
                          </Link>
                        )}
                      </li>
                    ))}
                  </ol>
                ),
              }))}
            />
          </Card>
        </Layout.Content>
      </Layout>
    </ConfigProvider>
  );
}
