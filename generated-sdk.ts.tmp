/**
 * SDK 生成的静态契约类型（人工维护，稳定，不会被 SDKHandler 覆盖）。
 * 契约定义见 HA-Contract/docs/dev/HRPAuth/sdk-package.md 的 "Generated Frontend Contract" 一节。
 * HA-WebUI-SDKHandler 在构建时按本文件类型生成 src/generated/ 下的模块。
 */
import type { ReactNode } from 'react';

/** 单个 SDK 服务包的清单条目。 */
export interface SdkManifestEntry {
  name: string;
  version: string;
  routes: { path: string; module: string; title?: string }[];
  menu?: { label: string; path: string }[];
  dashboard?: { label: string; path: string }[];
  profileCards?: { title: string; module: string; priority?: number }[];
  runScripts: boolean;
  sha256: string;
}

/** 导航栏动态菜单项（对应 manifest.menu）。 */
export interface SdkNavbarItem {
  key: string; // 服务名
  label: string;
  path: string;
}

/** Dashboard 侧栏动态项（对应 manifest.dashboard，主区渲染对应路由组件）。 */
export interface SdkDashboardItem {
  key: string; // 服务名
  label: string;
  element: ReactNode; // 懒加载的路由内容组件
}

/** Profile 页动态卡片（对应 manifest.profile_cards）。 */
export interface SdkProfileCardItem {
  key: string; // 服务名或服务名-序号
  title: string;
  priority?: number;
  loadOrder: number; // SDK handler 当前聚合顺序，供稳定排序
  element: ReactNode; // 懒加载的卡片内容组件
}
