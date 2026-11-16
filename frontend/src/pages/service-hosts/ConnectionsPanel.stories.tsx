import type { Meta, StoryObj } from '@storybook/react-vite';
import { ConnectionsPanel } from './ServiceHostsPage';
import { hostRuntimeFixture } from './fixtures';

const meta = {
  title: 'Hosts/Connection observations',
  component: ConnectionsPanel,
  args: { reports: [hostRuntimeFixture] },
} satisfies Meta<typeof ConnectionsPanel>;
export default meta;
type Story = StoryObj<typeof meta>;
export const RecentDials: Story = {};
export const ObserverUnavailable: Story = {
  args: {
    reports: [{ ...hostRuntimeFixture, observer: { ready: false, dropped: 0, records: [] } }],
  },
};
