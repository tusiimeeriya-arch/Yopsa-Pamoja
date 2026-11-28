import { q } from './db.js';

/**
 * Provider-neutral messaging.
 *
 * Two adapters:
 *   stub  records the message in outbound_messages and delivers nothing. A
 *         guarded action then fails closed with a clear message (see auth.js).
 *   http  POSTs JSON {to, from, text, channel} to SMS_HTTP_URL with an optional
 *         bearer token. Any HTTP API provider or SMTP-to-HTTP relay fits.
 *
 * The message body (which may contain a one-time code) is stored in
 * outbound_messages for delivery evidence but is never written to the process
 * log. Logs carry ids and metadata only.
 */
export const PROVIDER = process.env.SMS_PROVIDER || 'stub';
const HTTP_URL = process.env.SMS_HTTP_URL || '';
const HTTP_TOKEN = process.env.SMS_HTTP_TOKEN || '';
const HTTP_FROM = process.env.SMS_HTTP_FROM || 'Pamoja';

async function deliverHttp({ channel, recipient, body }) {
  if (!HTTP_URL) throw new Error('SMS_PROVIDER=http but SMS_HTTP_URL is not set');
  const res = await fetch(HTTP_URL, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      ...(HTTP_TOKEN ? { authorization: `Bearer ${HTTP_TOKEN}` } : {}),
    },
    body: JSON.stringify({ to: recipient, from: HTTP_FROM, text: body, channel }),
    signal: AbortSignal.timeout(10000),
  });
  if (!res.ok) throw new Error(`provider responded ${res.status}`);
}

export async function sendMessage({ channel = 'sms', recipient, body, userId = null }) {
  if (!['sms', 'whatsapp'].includes(channel)) throw new Error(`Unsupported channel: ${channel}`);
  const status = PROVIDER === 'stub' ? 'stub-recorded' : 'queued';
  const r = await q(
    `INSERT INTO outbound_messages (channel, recipient, body, provider, status)
     VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
    [channel, String(recipient || '').slice(0, 200), String(body).slice(0, 1000), PROVIDER, status],
  );
  const messageId = r.rows[0].id;

  if (PROVIDER === 'http') {
    try {
      await deliverHttp({ channel, recipient, body });
      await q("UPDATE outbound_messages SET status='sent' WHERE id=$1", [messageId]);
    } catch (err) {
      await q("UPDATE outbound_messages SET status='failed' WHERE id=$1", [messageId]);
      console.error(JSON.stringify({ level: 'error', message: 'outbound delivery failed', channel, provider: PROVIDER, messageId, userId, error: err.message }));
      throw err;
    }
  }

  console.log(JSON.stringify({ level: 'info', message: 'outbound message recorded', channel, provider: PROVIDER, messageId, userId }));
  return { id: messageId, channel, provider: PROVIDER, status: PROVIDER === 'http' ? 'sent' : 'stub-recorded' };
}

export async function sendSms(recipient, body) {
  return sendMessage({ channel: 'sms', recipient, body });
}
