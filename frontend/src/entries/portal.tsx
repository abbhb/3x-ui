import { createRoot } from 'react-dom/client';
import { message } from 'antd';
import 'antd/dist/reset.css';

import { readyI18n } from '@/i18n/react';
import { AntdRootConfig } from '@/i18n/AntdRootConfig';
import { ThemeProvider } from '@/hooks/useTheme';
import { QueryProvider } from '@/api/QueryProvider';
import PortalPage from '@/pages/portal/PortalPage';

const messageContainer = document.getElementById('message');
if (messageContainer) {
  message.config({ getContainer: () => messageContainer });
}

readyI18n().then(() => {
  const root = document.getElementById('app');
  if (root) {
    createRoot(root).render(
      <AntdRootConfig>
        <ThemeProvider>
          <QueryProvider>
            <PortalPage />
          </QueryProvider>
        </ThemeProvider>
      </AntdRootConfig>,
    );
  }
});
