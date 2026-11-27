import { q } from './db.js';

/**
 * Provider-neutral messaging stub.
 *
 * No third-party provider is called. Every message is recorded in
 * outbound_messages with provider='stub' so a real provider (SMS or WhatsApp)
 * can be added behind this single function without touching callers.
 */
export const PROVIDER = process.env.SMS_PROVIDER || 'stub';

export async function sendMessage({ channel = 'sms', recipient, body, userId = null }) {
  if (!['sms', 'whatsapp'].includes(channel)) throw new Error(`Unsupported channel: ${channel}`);
  const r = await q(
    `INSERT INTO outbound_messages (channel, recipient, body, provider, status)
     VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
    [channel, String(recipient || '').slice(0, 200), String(body).slice(0, 1000), PROVIDER, PROVIDER === 'stub' ? 'stub-recorded' : 'queued'],
  );
  console.log(JSON.stringify({ level: 'info', message: 'outbound message recorded', channel, provider: PROVIDER, messageId: r.rows[0].id, userId }));
  return { id: r.rows[0].id, channel, provider: PROVIDER, status: r.rows[0].status };
}

export async function sendSms(recipient, body) {
  return sendMessage({ channel: 'sms', recipient, body });
}
