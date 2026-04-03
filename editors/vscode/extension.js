const vscode = require('vscode');
const { LanguageClient, TransportKind } = require('vscode-languageclient/node');
const path = require('path');
const fs = require('fs');
const { execSync } = require('child_process');

let client;

function findLspBinary() {
    // 1. user config
    const config = vscode.workspace.getConfiguration('luxo');
    const configPath = config.get('lsp.path', '');
    if (configPath && fs.existsSync(configPath)) {
        return configPath;
    }

    // 2. GOPATH/bin
    try {
        const gopath = execSync('go env GOPATH', { encoding: 'utf8' }).trim();
        const gopathBin = path.join(gopath, 'bin', 'luxo-lsp');
        if (fs.existsSync(gopathBin)) {
            return gopathBin;
        }
    } catch (e) { /* go not installed */ }

    // 3. try PATH
    try {
        const which = execSync('which luxo-lsp', { encoding: 'utf8' }).trim();
        if (which) return which;
    } catch (e) { /* not in PATH */ }

    // 4. auto install
    return autoInstall();
}

function autoInstall() {
    try {
        vscode.window.showInformationMessage('Luxo: installing language server...');
        execSync('go install github.com/light-speak/luxo/cmd/luxo-lsp@latest', {
            encoding: 'utf8',
            timeout: 60000,
        });
        const gopath = execSync('go env GOPATH', { encoding: 'utf8' }).trim();
        const bin = path.join(gopath, 'bin', 'luxo-lsp');
        if (fs.existsSync(bin)) {
            vscode.window.showInformationMessage('Luxo: language server installed');
            return bin;
        }
    } catch (e) {
        vscode.window.showErrorMessage(
            'Luxo: failed to install language server. Run: go install github.com/light-speak/luxo/cmd/luxo-lsp@latest'
        );
    }
    return null;
}

function activate(context) {
    const lspPath = findLspBinary();
    if (!lspPath) {
        return;
    }

    const serverOptions = {
        command: lspPath,
        transport: TransportKind.stdio,
    };

    const clientOptions = {
        documentSelector: [{ scheme: 'file', language: 'luxo' }],
    };

    client = new LanguageClient('luxo', 'Luxo Language Server', serverOptions, clientOptions);
    client.start();
}

function deactivate() {
    if (client) {
        return client.stop();
    }
}

module.exports = { activate, deactivate };
