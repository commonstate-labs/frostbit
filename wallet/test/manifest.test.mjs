import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

test('the wallet manifest grants DApp access to FrostBit and loopback pages', () => {
    const manifest = JSON.parse(readFileSync(new URL('../manifest.json', import.meta.url), 'utf8'));
    const allowedMatches = new Set([
        'https://*.frostbit.io/*',
        'http://localhost/*',
        'http://localhost:*/*',
        'http://127.0.0.1/*',
        'http://127.0.0.1:*/*',
    ]);

    assert.deepEqual(new Set(manifest.content_scripts.flatMap(script => script.matches)), allowedMatches, 'content_scripts');
    assert.deepEqual(new Set(manifest.web_accessible_resources.flatMap(resource => resource.matches)), allowedMatches, 'web_accessible_resources');
    assert.deepEqual(new Set(manifest.host_permissions), allowedMatches, 'host_permissions');
});
