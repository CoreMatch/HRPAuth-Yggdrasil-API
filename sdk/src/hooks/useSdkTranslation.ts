import { useEffect } from 'react';
// @ts-ignore
import { useTranslation } from 'react-i18next';
import zhCN from '../locales/zh-CN.json';
import en from '../locales/en.json';

/**
 * Hook to inject SDK-specific translations into the host app's i18next instance.
 * This ensures the SDK components can use their own translation keys.
 */
export function useSdkTranslation() {
  const { i18n } = useTranslation();

  useEffect(() => {
    // Inject zh-CN
    if (!i18n.hasResourceBundle('zh-CN', 'sdk-yggdrasil')) {
      i18n.addResourceBundle('zh-CN', 'translation', zhCN, true, true);
    }
    // Inject en
    if (!i18n.hasResourceBundle('en', 'sdk-yggdrasil')) {
      i18n.addResourceBundle('en', 'translation', en, true, true);
    }
  }, [i18n]);

  return useTranslation();
}
