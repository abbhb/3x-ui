import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, within } from 'storybook/test';
import MGMAControls from './MGMAControls';
import './Commerce.css';
const meta = {
  title: 'Commerce/Subscription access',
  component: MGMAControls,
  args: {
    issue: async () => ({
      url: 'https://portal.example.test/sub/synthetic/?token=' + 'G'.repeat(43),
      issued_at: new Date().toISOString(),
      expires_at: null,
      ttl_seconds: 0,
    }),
    revoke: async () => {},
    regenerate: async () => {},
  },
} satisfies Meta<typeof MGMAControls>;
export default meta;
type Story = StoryObj<typeof meta>;
export const BeforeActivation: Story = { args: { disabled: true } };
export const PermanentLink: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: 'Generate link' }));
    await expect(canvas.getByRole('textbox', { name: 'Subscription access' })).toBeVisible();
  },
};
