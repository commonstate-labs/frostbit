import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const inpageSource = fs.readFileSync(new URL('../content/inpage.js', import.meta.url), 'utf8');

function loadProvider() {
    const listeners = new Map();
    const outbound = [];
    const window = {
        addEventListener(type, listener) {
            listeners.set(type, listener);
        },
        postMessage(message, targetOrigin) {
            outbound.push({ message, targetOrigin });
        },
        dispatchEvent() {},
    };

    class CustomEvent {
        constructor(type) {
            this.type = type;
        }
    }

    vm.runInNewContext(inpageSource, { window, CustomEvent });
    return { window, listeners, outbound };
}

test('signMessage preserves the exact message and returns signer metadata', async () => {
    const { window, listeners, outbound } = loadProvider();
    const message = 'FrostBit admin challenge\nnonce=abc123\n';
    const pending = window.frostbit.signMessage(message);

    assert.equal(outbound.length, 1);
    assert.equal(outbound[0].targetOrigin, '*');
    assert.equal(outbound[0].message.msg.type, 'SIGN_MESSAGE');
    assert.equal(outbound[0].message.msg.message, message);

    const response = {
        signature: 'signature-base64',
        address: 'fbc1qsigner',
        publicKey: 'compressed-public-key-base64',
    };
    listeners.get('message')({
        source: window,
        data: {
            __frostbit_dir: 'from_cs',
            id: outbound[0].message.id,
            result: response,
        },
    });

    const result = await pending;
    assert.equal(result.signature, response.signature);
    assert.equal(result.address, response.address);
    assert.equal(result.publicKey, response.publicKey);
});
