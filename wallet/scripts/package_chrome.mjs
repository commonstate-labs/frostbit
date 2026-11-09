import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const walletDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = path.resolve(walletDir, '..');
const manifestPath = path.join(walletDir, 'manifest.json');
const packagePath = path.join(walletDir, 'package.json');
const lockPath = path.join(walletDir, 'package-lock.json');
const distDir = path.join(walletDir, 'dist');
const outDir = path.join(repoDir, 'dist');

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function indentOf(file) {
  const match = fs.readFileSync(file, 'utf8').match(/\n( +)"/);
  return match ? match[1].length : 2;
}

function writeJson(file, data) {
  fs.writeFileSync(file, `${JSON.stringify(data, null, indentOf(file))}\n`);
}

function nextVersion(version) {
  const parts = String(version).split('.');
  if (parts.length < 1 || parts.length > 4 || parts.some(p => !/^\d+$/.test(p))) {
    throw new Error(`Chrome manifest version must be 1-4 dot-separated integers: ${version}`);
  }
  while (parts.length < 3) parts.push('0');
  parts[parts.length - 1] = String(Number(parts[parts.length - 1]) + 1);
  return parts.join('.');
}

function run(command, args, cwd = walletDir) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit', shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} failed with exit code ${result.status}`);
}

function runNpmScript(name) {
  if (process.platform === 'win32') {
    run('cmd.exe', ['/d', '/s', '/c', `npm run ${name}`]);
    return;
  }
  run('npm', ['run', name]);
}

function psQuote(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function zipDist(outZip) {
  fs.mkdirSync(outDir, { recursive: true });
  fs.rmSync(outZip, { force: true });
  if (process.platform === 'win32') {
    const source = path.join(distDir, '*');
    run('powershell.exe', [
      '-NoProfile',
      '-ExecutionPolicy',
      'Bypass',
      '-Command',
      `$ErrorActionPreference='Stop'; Compress-Archive -Path ${psQuote(source)} -DestinationPath ${psQuote(outZip)} -Force`,
    ], repoDir);
    return;
  }
  run('zip', ['-qr', outZip, '.'], distDir);
}

const versionFiles = [manifestPath, packagePath, lockPath].filter(file => fs.existsSync(file));
const originals = new Map(versionFiles.map(file => [file, fs.readFileSync(file)]));

try {
  const manifest = readJson(manifestPath);
  const pkg = readJson(packagePath);
  const version = nextVersion(manifest.version);
  manifest.version = version;
  pkg.version = version;
  writeJson(manifestPath, manifest);
  writeJson(packagePath, pkg);

  if (fs.existsSync(lockPath)) {
    const lock = readJson(lockPath);
    lock.version = version;
    if (lock.packages?.['']) lock.packages[''].version = version;
    writeJson(lockPath, lock);
  }

  console.log(`Wallet version bumped to ${version}`);
  runNpmScript('build');

  const outZip = path.join(outDir, `frostbit-wallet-v${version}-chrome.zip`);
  zipDist(outZip);
  console.log(`Chrome Web Store zip created: ${outZip}`);
} catch (error) {
  for (const [file, content] of originals) fs.writeFileSync(file, content);
  console.error(error);
  process.exit(1);
}
