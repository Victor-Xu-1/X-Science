import type { XScienceProviderFailureKind } from './runtimeOperationsModel';

/** Closed presentation of an existing pause reason, never a lifecycle decision. */
export function runtimePausePresentation(
  reason: string | null,
  providerFailureKind?: XScienceProviderFailureKind | null
): { title: string; description: string } {
  switch (reason) {
    case 'model_provider_unavailable':
      switch (providerFailureKind) {
        case 'configuration_missing':
          return { title: 'taskWaitingModel', description: 'providerConfigurationPaused' };
        case 'subscription':
          return { title: 'providerSubscriptionUnavailable', description: 'providerSubscriptionPaused' };
        case 'authentication':
          return { title: 'providerAuthenticationFailed', description: 'providerAuthenticationPaused' };
        case 'access_denied':
          return { title: 'providerAccessDenied', description: 'providerAccessPaused' };
        default:
          return { title: 'providerUnavailable', description: 'providerUnavailablePaused' };
      }
    case 'provider_stream_no_progress':
      return { title: 'modelResponsePaused', description: 'modelNoProgressPaused' };
    case 'provider_output_token_limit':
      return { title: 'taskPaused', description: 'outputLimitPaused' };
    default:
      return { title: 'taskPaused', description: 'recoveryPaused' };
  }
}
