import { Message } from '@arco-design/web-react';
import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { AcpConfigSetStatus, AcpDerivedOption } from '@/renderer/hooks/xScience/runtime/useAcpConfigOptions';
import {
  useSessionPermissionGrantsController,
  useSessionPermissionGrantsView,
} from '@/renderer/hooks/xScience/runtime/useSessionPermissionGrants';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import type { SessionGrantIdentity } from '@/renderer/services/runtime/sessionPermissionGrants';
import type { SessionPermissionGrantsController } from '@/renderer/services/runtime/sessionPermissionGrantsController';
import { isWarningPermissionMode } from './XSciencePermissionIcon';

export type XSciencePermissionControlInput = {
  option: AcpDerivedOption | null;
  setStatus: AcpConfigSetStatus;
  setConfigOption: (optionId: string, value: string) => Promise<unknown>;
  includeStandardModes?: boolean;
  disabled?: boolean;
  sessionIdentity?: SessionGrantIdentity;
  grantsController?: SessionPermissionGrantsController | null;
  contextKey?: string;
};

const STANDARD_MODES = [
  { value: 'default', label: 'Default' },
  { value: 'smart', label: 'Smart' },
  { value: 'bypassPermissions', label: 'Full access' },
];

/** Presentation/admission adapter only: the existing runtime setter and grant
 * controller remain the sole mutation/observation owners in either menu shell. */
export function useXSciencePermissionControl(input?: XSciencePermissionControlInput, closeMenu?: () => void) {
  const { i18n, t } = useTranslation();
  const resetId = useId();
  const localController = useSessionPermissionGrantsController(input?.grantsController ? null : input?.sessionIdentity);
  const controller = input?.grantsController ?? localController;
  const grants = useSessionPermissionGrantsView(controller);
  const optionId = input?.option?.id ?? 'mode';
  const account = getRendererAccountScopeToken();
  const owner = useMemo(
    () => ({
      account,
      contextKey: input?.contextKey ?? controller?.presentationKey ?? 'permission-mode',
      optionId,
      setter: input?.setConfigOption,
    }),
    [account, controller?.presentationKey, input?.contextKey, input?.setConfigOption, optionId]
  );
  const currentOwner = useRef(owner);
  currentOwner.current = owner;
  const mount = useRef({ mounted: false, generation: 0 });
  const inFlight = useRef<{ owner: typeof owner } | null>(null);
  const [pendingOwner, setPendingOwner] = useState<typeof owner | null>(null);
  const [confirmation, setConfirmation] = useState<{ owner: typeof owner; generation: number; value: string } | null>(
    null
  );
  const closeRef = useRef(closeMenu);
  closeRef.current = closeMenu;
  const currentValue = input?.option?.currentValue ?? 'default';
  const options = useMemo(() => {
    const seen = new Set<string>();
    return [...(input?.includeStandardModes ? STANDARD_MODES : []), ...(input?.option?.options ?? [])]
      .filter((item) => {
        if (seen.has(item.value)) return false;
        seen.add(item.value);
        return true;
      })
      .map((item) => ({
        value: item.value,
        label:
          item.value === 'default'
            ? t('agentMode.requestApproval')
            : item.value === 'smart'
              ? t('agentMode.helpApprove')
              : item.value === 'bypassPermissions'
                ? t('agentMode.fullAccess')
                : i18n.exists(`agentMode.${item.value}`)
                  ? t(`agentMode.${item.value}`)
                  : item.label,
        description: isWarningPermissionMode(item.value)
          ? t('agentMode.fullAccessDescription')
          : ['smart', 'auto', 'dontAsk', 'autoEdit'].includes(item.value)
            ? t('agentMode.riskyOperationsDescription')
            : item.value === 'default'
              ? t('agentMode.approvalRequiredDescription')
              : ('description' in item && item.description) || t('agentMode.approvalRequiredDescription'),
      }));
  }, [i18n, input?.includeStandardModes, input?.option?.options, t]);
  useEffect(() => {
    setConfirmation((current) => (current && !options.some((item) => item.value === current.value) ? null : current));
  }, [options]);
  const unavailable =
    !input ||
    input.disabled ||
    input.setStatus.state === 'setting' ||
    pendingOwner === owner ||
    Boolean(controller && !controller.isCurrent());

  useEffect(() => {
    mount.current.mounted = true;
    const release = registerRendererAccountReset(`permission-control-${resetId}`, () => {
      setConfirmation(null);
      closeRef.current?.();
    });
    return () => {
      mount.current.mounted = false;
      mount.current.generation += 1;
      release();
    };
  }, [resetId]);
  const previousOwner = useRef(owner);
  useEffect(() => {
    if (previousOwner.current === owner) return;
    previousOwner.current = owner;
    setConfirmation(null);
    closeRef.current?.();
  }, [owner]);

  const isCurrent = (requestOwner: typeof owner, generation: number) =>
    mount.current.mounted &&
    mount.current.generation === generation &&
    currentOwner.current === requestOwner &&
    getRendererAccountScopeToken() === requestOwner.account &&
    (!controller || controller.isCurrent());
  const perform = async (value: string) => {
    if (unavailable || value === currentValue || !options.some((item) => item.value === value) || !input) return;
    if (inFlight.current?.owner === owner) return;
    const generation = mount.current.generation;
    if (!isCurrent(owner, generation)) return;
    const claim = { owner };
    inFlight.current = claim;
    setPendingOwner(owner);
    try {
      await input.setConfigOption(optionId, value);
      if (isCurrent(owner, generation)) Message.success(t('agentMode.switchSuccess'));
    } catch (error) {
      if (isCurrent(owner, generation)) {
        console.error('Failed to set X-Science permission mode', error);
        Message.error(t('agentMode.switchFailed'));
      }
    } finally {
      if (inFlight.current === claim) inFlight.current = null;
      if (isCurrent(owner, generation)) setPendingOwner(null);
    }
  };
  const select = (value: string) => {
    if (unavailable || value === currentValue || !options.some((item) => item.value === value)) return;
    closeRef.current?.();
    if (isWarningPermissionMode(value)) {
      setConfirmation({ owner, generation: mount.current.generation, value });
    } else void perform(value);
  };
  const confirm = () => {
    const request = confirmation;
    setConfirmation(null);
    if (request && isCurrent(request.owner, request.generation)) void perform(request.value);
  };
  return {
    controller,
    grants,
    options,
    currentValue,
    selectedOption: options.find((item) => item.value === currentValue),
    contextKey: owner,
    available: options.length > 0 || Boolean(controller?.isCurrent()),
    unavailable,
    select,
    confirm,
    cancelConfirmation: () => setConfirmation(null),
    confirmationVisible: Boolean(
      confirmation &&
      confirmation.owner === owner &&
      options.some((item) => item.value === confirmation.value) &&
      isCurrent(owner, confirmation.generation)
    ),
  };
}

export type XSciencePermissionControl = ReturnType<typeof useXSciencePermissionControl>;
