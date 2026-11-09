export const WALLET_ENV_KEY = 'frostbit_wallet_env_v1';
export const WALLET_ENV_CONFIGS_KEY = 'frostbit_wallet_env_configs_v1';
export const DEFAULT_WALLET_ENV = 'dev';

export const WALLET_ENV_PROFILES = Object.freeze([
    { key: 'dev', explorerUrl: 'http://localhost:8080', nodeAddr: '' },
    { key: 'test', explorerUrl: 'https://test.public-network.invalid', nodeAddr: '' },
    { key: 'prod', explorerUrl: 'https://app.public-network.invalid', nodeAddr: '' },
]);

export function normalizeWalletEnv(value) {
    return WALLET_ENV_PROFILES.some(p => p.key === value) ? value : DEFAULT_WALLET_ENV;
}

export function defaultEnvConfig(env) {
    const profile = WALLET_ENV_PROFILES.find(p => p.key === normalizeWalletEnv(env)) || WALLET_ENV_PROFILES[0];
    return { explorerUrl: profile.explorerUrl, nodeAddr: profile.nodeAddr || '' };
}

export function normalizeEnvConfig(env, config = {}) {
    const fallback = defaultEnvConfig(env);
    const explorerUrl = typeof config.explorerUrl === 'string' ? config.explorerUrl.trim() : '';
    const nodeAddr = typeof config.nodeAddr === 'string' ? config.nodeAddr.trim() : '';
    return {
        explorerUrl: explorerUrl || fallback.explorerUrl,
        nodeAddr,
    };
}

export function mergeWalletEnvConfigs(stored = {}) {
    const source = stored && typeof stored === 'object' ? stored : {};
    return Object.fromEntries(
        WALLET_ENV_PROFILES.map(profile => [profile.key, normalizeEnvConfig(profile.key, source[profile.key])])
    );
}
