export const money = (minor: number) =>
  new Intl.NumberFormat(undefined, { style: 'currency', currency: 'CNY' }).format(minor / 100);
export const date = (ms: number) => (ms ? new Date(ms).toLocaleString() : '—');
export const bytes = (value: number) => {
  if (value < 1024) return `${value} B`;
  const unit = Math.min(4, Math.floor(Math.log(value) / Math.log(1024)));
  return `${(value / 1024 ** unit).toFixed(2)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][unit]}`;
};
