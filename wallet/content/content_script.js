   
                                              
                                                                                                           
                                                                                           
   
import { sanitizeDappMessage } from '../lib/message_policy.js';

(function () {
                                                                                 
    const ALLOWED = [
        /^https:\/\/([a-z0-9-]+\.)*frostbit\.io$/i,
        /^http:\/\/localhost(:\d+)?$/i,
        /^http:\/\/127\.0\.0\.1(:\d+)?$/i,
    ];
    const originOk = (o) => !!o && ALLOWED.some(re => re.test(o));

                                                                                                                                               
    const script = document.createElement('script');
    script.src = chrome.runtime.getURL('content/inpage.js');
    script.onload = () => script.remove();
    (document.head || document.documentElement).appendChild(script);

                                                                                                                  
    window.addEventListener('message', (e) => {
        if (e.source !== window || !e.data || e.data.__frostbit_dir !== 'to_bg') return;
        if (!originOk(e.origin)) { console.warn('[FrostBit] 忽略非白名单 origin:', e.origin); return; }
        const { id, msg } = e.data;
        const targetOrigin = window.location.origin;
        try {
                                                                          
                                                                   
            const publicMessage = sanitizeDappMessage(msg);
            chrome.runtime.sendMessage(publicMessage, (res) => {
                const error = chrome.runtime.lastError?.message || res?.error || null;
                window.postMessage({
                    __frostbit_dir: 'from_cs',
                    id,
                    result: error ? null : res,
                    error,
                }, targetOrigin);
            });
        } catch (err) {
            window.postMessage({
                __frostbit_dir: 'from_cs',
                id,
                result: null,
                error: err?.message || 'Wallet request rejected',
            }, targetOrigin);
        }
    });
})();
