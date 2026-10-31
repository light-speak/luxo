const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

test('cache policy watcher is passed to the client and disposed with the extension', async () => {
    const watcher = { dispose() { this.disposed = true; } };
    const context = { subscriptions: [] };
    let options;
    let stopped = false;
    const mocks = {
        vscode: {
            workspace: {
                getConfiguration: () => ({ get: () => '/configured/luxo-lsp' }),
                createFileSystemWatcher: (glob) => {
                    assert.equal(glob, '**/luxo.cache.json');
                    return watcher;
                },
            },
            window: { createOutputChannel: () => ({ appendLine() {} }) },
        },
        'vscode-languageclient/node': {
            LanguageClient: class {
                constructor(_id, _name, _server, clientOptions) { options = clientOptions; }
                async start() {}
                async stop() { stopped = true; }
            },
            TransportKind: { stdio: 0 },
        },
        path,
        fs: { existsSync: () => true },
        child_process: { execSync() { throw new Error('test must not run external commands'); } },
    };
    const sandbox = { module: { exports: {} }, require: (name) => {
        assert.ok(Object.hasOwn(mocks, name), `unexpected dependency: ${name}`);
        return mocks[name];
    } };
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'extension.js'), 'utf8'), sandbox);
    sandbox.module.exports.activate(context);
    assert.equal(options.synchronize.fileEvents, watcher);
    assert.ok(context.subscriptions.includes(watcher));
    await sandbox.module.exports.deactivate();
    assert.equal(stopped, true);
    for (const disposable of context.subscriptions) disposable.dispose();
    assert.equal(watcher.disposed, true);
});
