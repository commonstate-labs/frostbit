import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8');
const globalStyleSource = await readFile(new URL('../src/style.css', import.meta.url), 'utf8');
const backgroundSource = await readFile(new URL('../background/service_worker.js', import.meta.url), 'utf8');

test('the regular popup keeps its fixed 380px content width', () => {
    assert.match(globalStyleSource, /body\s*\{[^{}]*\bwidth:\s*380px\s*;/s);
    assert.match(appSource, /\.wallet-shell\s*\{[^{}]*\bwidth:\s*380px\s*;/s);
});

test('the approval popup keeps its fixed 520px content width', () => {
    assert.match(globalStyleSource, /body\.approval-window\s*\{[^{}]*\bwidth:\s*520px\s*;/s);
    assert.match(appSource, /\.wallet-shell\.approval-shell\s*\{[^{}]*\bwidth:\s*520px\s*;/s);
});

test('the background approval window keeps its 540x720 outer dimensions', () => {
    assert.match(backgroundSource, /const\s+APPROVAL_POPUP_WIDTH\s*=\s*540\s*;/);
    assert.match(backgroundSource, /const\s+APPROVAL_POPUP_HEIGHT\s*=\s*720\s*;/);
    assert.match(backgroundSource, /chrome\.windows\.create\s*\(\s*\{[^{}]*\bwidth:\s*APPROVAL_POPUP_WIDTH\s*,[^{}]*\bheight:\s*APPROVAL_POPUP_HEIGHT\s*,/s);
});

test('a DApp-launched approval window uses the wide layout before the wallet is unlocked', () => {
    assert.match(backgroundSource, /const\s+APPROVAL_POPUP_URL\s*=\s*['"]popup\/index\.html\?mode=approval['"]\s*;/);
    assert.match(backgroundSource, /chrome\.windows\.create\s*\(\s*\{[^{}]*\burl:\s*APPROVAL_POPUP_URL\s*,/s);
    assert.match(appSource, /const\s+isApprovalWindow\s*=\s*typeof window\s*!==\s*['"]undefined['"][\s\S]*?new URLSearchParams\(window\.location\.search\)\.get\(['"]mode['"]\)\s*===\s*['"]approval['"]/);
    assert.match(appSource, /const\s+isApprovalLayout\s*=\s*computed\(\(\)\s*=>\s*isApprovalWindow\s*\|\|\s*isApprovalPage\.value\)/);
    assert.match(appSource, /watch\(isApprovalLayout,/);
    assert.match(appSource, /['"]approval-shell['"]:\s*isApprovalLayout/);
});

test('popup roots do not use viewport-relative max-width', () => {
    const styleSources = `${globalStyleSource}\n${appSource}`;
    const rootRules = [...styleSources.matchAll(/(?:^|\n)\s*(?::global\(\s*body\s*\)|body|\.wallet-shell)\s*\{([^{}]*)\}/g)];
    assert.ok(rootRules.length >= 3, 'expected body and wallet-shell root rules');
    for (const [, declarations] of rootRules) {
        assert.doesNotMatch(declarations, /max-width:\s*100vw\s*;/, 'fixed popup roots must not follow their own viewport width');
    }
});
