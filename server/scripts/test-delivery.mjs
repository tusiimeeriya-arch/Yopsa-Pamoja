/**
 * Send one test message through the configured provider.
 *
 *   TEST_DELIVERY_TO=+2567... node scripts/test-delivery.mjs
 *
 * Exits 0 only when a provider actually accepted the message. With the stub it
 * exits 1: nothing was delivered. The body carries a fixed test marker, never a
 * real one-time code, and no code is printed.
 */
import process from 'node:process';
import { sendMessage, PROVIDER } from '../src/notify.js';

const recipient = process.env.TEST_DELIVERY_TO || '';
if (!recipient) {
  console.error('TEST_DELIVERY_TO is required (a phone number or email address you control)');
  process.exit(2);
}

if (PROVIDER === 'stub') {
  console.error('SMS_PROVIDER=stub: no provider is configured, so nothing can be delivered. Set SMS_PROVIDER=http and SMS_HTTP_URL first.');
  process.exit(1);
}

try {
  const result = await sendMessage({
    channel: 'sms',
    recipient,
    body: 'Pamoja delivery test. This is a test message, not a confirmation code.',
  });
  console.log(JSON.stringify({ provider: result.provider, status: result.status, messageId: result.id }));
  process.exit(result.status === 'sent' ? 0 : 1);
} catch (err) {
  console.error(JSON.stringify({ provider: PROVIDER, status: 'failed', error: err.message }));
  process.exit(1);
}
