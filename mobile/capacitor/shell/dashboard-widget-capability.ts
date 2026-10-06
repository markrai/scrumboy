import { DASHBOARD_WIDGET_CAPABILITY, type DashboardWidgetCapability, type DashboardWidgetSnapshotPayload } from '../../../internal/httpapi/web/modules/platform/dashboard-widget.js';
import type { ScrumboyTransportPlugin } from './native-plugin.js';

export function createDashboardWidgetCapability(plugin: ScrumboyTransportPlugin): DashboardWidgetCapability {
  return {
    setCurrentUser: (userId) => plugin.setDashboardWidgetCurrentUser({ userId }),
    publish: (snapshot: DashboardWidgetSnapshotPayload) => plugin.publishDashboardWidgetSnapshot(snapshot),
    clear: () => plugin.clearDashboardWidgetSnapshot(),
  };
}

export { DASHBOARD_WIDGET_CAPABILITY };
