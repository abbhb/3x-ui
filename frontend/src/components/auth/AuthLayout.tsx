import { useCallback, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Layout, Menu, Popover, Space } from 'antd';
import { MoonFilled, MoonOutlined, SunOutlined, TranslationOutlined } from '@ant-design/icons';
import { pauseAnimationsUntilLeave, useTheme } from '@/hooks/useTheme';
import { LanguageManager } from '@/utils';
import './AuthLayout.css';

export default function AuthLayout({ title, children }: { title: string; children: ReactNode }) {
  const { t } = useTranslation();
  const { isDark, isUltra, toggleTheme, toggleUltra } = useTheme();
  const [lang, setLang] = useState<string>(() => LanguageManager.getLanguage());

  const onLangChange = useCallback((next: string) => {
    setLang(next);
    LanguageManager.setLanguage(next);
  }, []);

  const cycleTheme = useCallback(() => {
    pauseAnimationsUntilLeave('login-theme-cycle');
    if (!isDark) {
      toggleTheme();
      if (isUltra) toggleUltra();
    } else if (!isUltra) {
      toggleUltra();
    } else {
      toggleUltra();
      toggleTheme();
    }
  }, [isDark, isUltra, toggleTheme, toggleUltra]);

  const pageClass = useMemo(() => {
    const classes = ['login-app'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  const langMenuItems = useMemo(
    () =>
      (LanguageManager.supportedLanguages as { value: string; name: string; icon: string }[]).map(
        (l) => ({
          key: l.value,
          label: (
            <Space size={8}>
              <span aria-hidden="true">{l.icon}</span>
              <span>{l.name}</span>
            </Space>
          ),
        }),
      ),
    [],
  );

  const themeIcon = !isDark ? (
    <SunOutlined aria-hidden />
  ) : !isUltra ? (
    <MoonOutlined aria-hidden />
  ) : (
    <MoonFilled aria-hidden />
  );

  return (
    <Layout className={pageClass}>
      <Layout.Content className="login-content">
        <div className="login-toolbar">
          <Button
            id="login-theme-cycle"
            shape="circle"
            size="large"
            className="toolbar-btn"
            aria-label={t('menu.theme')}
            title={t('menu.theme')}
            icon={themeIcon}
            onClick={cycleTheme}
          />
          <Popover
            rootClassName={isDark ? 'dark' : 'light'}
            placement="bottomRight"
            trigger="click"
            styles={{ content: { padding: 4 } }}
            content={
              <Menu
                mode="vertical"
                selectable
                selectedKeys={[lang]}
                items={langMenuItems}
                onClick={({ key }) => onLangChange(key)}
                style={{ border: 'none', minWidth: 160 }}
              />
            }
          >
            <Button
              shape="circle"
              size="large"
              className="toolbar-btn"
              aria-label={t('pages.settings.language')}
              icon={<TranslationOutlined aria-hidden />}
            />
          </Popover>
        </div>

        <div className="login-wrapper">
          <div className="login-card">
            <h1 className="auth-title">{title}</h1>
            <span className="brand-accent" aria-hidden="true" />
            {children}
          </div>
        </div>
      </Layout.Content>
    </Layout>
  );
}
