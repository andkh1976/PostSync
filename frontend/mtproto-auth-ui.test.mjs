import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import assert from 'node:assert/strict';

const html = fs.readFileSync(path.resolve('frontend/index.html'), 'utf8');

test('waiting_password branch keeps recovery within auth step', () => {
  const waitingPasswordBlock = html.match(/if \(status === 'waiting_password'\) \{[\s\S]*?return;\s*\}/);
  assert.ok(waitingPasswordBlock, 'expected dedicated waiting_password UI branch');

  assert.match(
    waitingPasswordBlock[0],
    /showStatus\('tg-auth-status',[\s\S]*Введите облачный пароль Telegram/,
    'expected waiting_password branch to show password-specific guidance in auth step',
  );

  assert.doesNotMatch(
    waitingPasswordBlock[0],
    /restartMtprotoAuth\(/,
    'waiting_password must not force full auth restart',
  );
});
