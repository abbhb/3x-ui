import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, ConfigProvider, Form, Input, Spin, message } from 'antd';
import { KeyOutlined, LockOutlined, UserOutlined } from '@ant-design/icons';
import { FormProvider, useForm } from 'react-hook-form';
import { HttpUtil } from '@/utils';
import { FormField, rhfZodValidate } from '@/components/form/rhf';
import AuthLayout from '@/components/auth/AuthLayout';
import { setMessageInstance } from '@/utils/messageBus';
import { useTheme } from '@/hooks/useTheme';
import { LoginFormSchema, TwoFactorCodeSchema, type LoginFormValues } from '@/schemas/login';

export default function LoginPage() {
  const { t } = useTranslation();
  const { antdThemeConfig } = useTheme();
  const [messageApi, messageContextHolder] = message.useMessage();

  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  const [fetched, setFetched] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [twoFactorEnable, setTwoFactorEnable] = useState(false);
  const methods = useForm<LoginFormValues>({
    defaultValues: { username: '', password: '', twoFactorCode: '' },
  });
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const msg = await HttpUtil.post('/getTwoFactorEnable');
      if (cancelled) return;
      if (msg.success) setTwoFactorEnable(!!msg.obj);
      setFetched(true);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const onSubmit = useCallback(async (values: LoginFormValues) => {
    setSubmitting(true);
    try {
      const msg = await HttpUtil.post('/login', values);
      if (msg.success) window.location.reload();
    } finally {
      setSubmitting(false);
    }
  }, []);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <AuthLayout title={t('menu.adminLogin')}>
        {!fetched ? (
          <div className="login-loading">
            <Spin size="large" />
          </div>
        ) : (
          <FormProvider {...methods}>
            <Form
              layout="vertical"
              className="login-form"
              onFinish={methods.handleSubmit(onSubmit)}
            >
              <FormField
                name="username"
                label={t('username')}
                rules={{ validate: rhfZodValidate(LoginFormSchema.shape.username) }}
              >
                <Input
                  prefix={<UserOutlined aria-hidden />}
                  autoComplete="username"
                  size="large"
                  placeholder={t('username')}
                  autoFocus
                />
              </FormField>

              <FormField
                name="password"
                label={t('password')}
                rules={{ validate: rhfZodValidate(LoginFormSchema.shape.password) }}
              >
                <Input.Password
                  prefix={<LockOutlined aria-hidden />}
                  autoComplete="current-password"
                  size="large"
                  placeholder={t('password')}
                />
              </FormField>

              {twoFactorEnable && (
                <FormField
                  name="twoFactorCode"
                  label={t('twoFactorCode')}
                  rules={{ validate: rhfZodValidate(TwoFactorCodeSchema) }}
                >
                  <Input
                    prefix={<KeyOutlined aria-hidden />}
                    autoComplete="one-time-code"
                    size="large"
                    placeholder={t('twoFactorCode')}
                  />
                </FormField>
              )}

              <Form.Item className="submit-row">
                <Button type="primary" htmlType="submit" loading={submitting} size="large" block>
                  {t('login')}
                </Button>
              </Form.Item>
            </Form>
          </FormProvider>
        )}
      </AuthLayout>
    </ConfigProvider>
  );
}
