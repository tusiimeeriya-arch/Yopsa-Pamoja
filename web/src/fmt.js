export const money = (n) => 'UGX ' + Number(n || 0).toLocaleString('en-UG');
export const num = (n) => Number(n || 0).toLocaleString('en-UG');
export const date = (v) => v ? new Date(v).toLocaleDateString('en-GB', { timeZone: 'Africa/Kampala', day: '2-digit', month: 'short', year: 'numeric' }) : '—';
export const dateTime = (v) => v ? new Date(v).toLocaleString('en-GB', { timeZone: 'Africa/Kampala', day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—';
export const time = (v) => v ? new Date(v).toLocaleTimeString('en-GB', { timeZone: 'Africa/Kampala', hour: '2-digit', minute: '2-digit' }) : '—';
export const pct = (v) => v === null || v === undefined ? 'Not applicable' : `${v}%`;
export const todayLocal = () => new Date().toLocaleDateString('en-CA', { timeZone: 'Africa/Kampala' });
export function csvDownload(text, filename) {
  const blob = new Blob([text], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url; a.download = filename; a.click();
  URL.revokeObjectURL(url);
}
