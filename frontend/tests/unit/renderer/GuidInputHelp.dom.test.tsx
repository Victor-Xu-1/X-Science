import { DEFAULT_LANGUAGE } from '@/common/config/i18n';
import ComposerInputHelp from '@/renderer/components/chat/SendBox/ComposerInputHelp';
import GuidInputCard from '@/renderer/pages/guid/components/GuidInputCard';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const layout = vi.hoisted(() => ({ isMobile: false }));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({ useLayoutContext: () => layout }));
vi.mock('@/renderer/components/media/FilePreview', () => ({ default: (): null => null }));
vi.mock('@/renderer/components/media/UploadProgressBar', () => ({ default: (): null => null }));

afterEach(cleanup);
beforeEach(() => {
  layout.isMobile = false;
});

describe('Guid input help', () => {
  it('omits unsupported reference and history instructions from limited-capability help', async () => {
    const limitedCapabilities = { fileReferences: false, historyNavigation: false };
    await renderWithI18n(
      <ComposerInputHelp
        {...limitedCapabilities}
        descriptionId='limited-help'
        promptHint='Start a task'
        sessionReferences={false}
      />,
      'en-US'
    );
    const description = document.getElementById('limited-help')!;
    expect(description).not.toHaveTextContent('@');
    expect(description).not.toHaveTextContent('#');
    expect(description).not.toHaveTextContent('Arrow');
    expect(description).toHaveTextContent('Type / for commands.');
    expect(description).toHaveTextContent('Enter to send.');
    expect(description).toHaveTextContent('Shift+Enter for a new line.');
  });

  it.each([
    [DEFAULT_LANGUAGE, 'Input help', 'Type / for commands.', 'Enter to send.', 'Shift+Enter for a new line.'],
    ['zh-CN', '输入帮助', '输入 / 唤起命令。', '按 Enter 发送。', '按 Shift+Enter 换行。'],
  ] as const)(
    'preserves the draft and input owner while exposing complete keyboard help in %s',
    async (language, label, commands, send, newLine) => {
      expect(DEFAULT_LANGUAGE).toBe('en-US');
      layout.isMobile = language === 'zh-CN';
      const user = userEvent.setup();
      const changed = vi.fn();
      const keyDown = vi.fn();
      const submit = vi.fn();
      const draft = 'Keep my draft. '.repeat(30);
      const Composer = () => {
        const [value, setValue] = React.useState(draft);
        return (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              submit();
            }}
          >
            <GuidInputCard
              input={value}
              onInputChange={(next) => {
                changed(next);
                setValue(next);
              }}
              onKeyDown={keyDown}
              onPaste={vi.fn()}
              onFocus={vi.fn()}
              onBlur={vi.fn()}
              placeholder='Animat'
              isInputActive={false}
              isFileDragging={false}
              activeBorderColor='var(--color-border-3)'
              inactiveBorderColor='var(--color-border-2)'
              activeShadow='none'
              inactiveShadow='none'
              surfaceBackgroundColor='transparent'
              dragHandlers={{}}
              files={[]}
              onRemoveFile={vi.fn()}
              actionRow={<button type='submit'>Send task</button>}
            />
          </form>
        );
      };
      const view = await renderWithI18n(<Composer />, language);
      const input = screen.getByTestId('guid-input');
      const help = screen.getByRole('button', { name: label });
      const descriptionId = input.getAttribute('aria-describedby');
      expect(descriptionId).toBeTruthy();
      const fullPrompt = view.i18n.t('conversation.welcome.placeholder');
      const description = document.getElementById(descriptionId!)!;
      expect(description).toHaveTextContent(fullPrompt);
      expect(description).not.toHaveTextContent('Animat');
      expect(description).toHaveTextContent(commands);
      expect(description).toHaveTextContent(send);
      expect(description).toHaveTextContent(newLine);
      expect(description).not.toHaveTextContent('@');
      expect(description).not.toHaveTextContent('#');
      expect(description).not.toHaveTextContent(view.i18n.t('conversation.sendbox.inputHelp.history'));

      fireEvent.compositionStart(input);
      fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
      fireEvent.compositionEnd(input);
      expect(keyDown).not.toHaveBeenCalled();
      input.focus();
      await user.tab();
      expect(help).toHaveFocus();
      keyDown.mockClear();
      await user.keyboard('{Enter}');
      expect(screen.getByRole('dialog', { name: label })).toHaveTextContent(fullPrompt);
      expect(screen.getByRole('dialog', { name: label })).toHaveTextContent(commands);
      await user.keyboard('{Escape}');
      await waitFor(() => expect(screen.queryByRole('dialog', { name: label })).not.toBeInTheDocument());
      expect(help).toHaveFocus();
      expect(input).toHaveValue(draft);
      expect(changed).not.toHaveBeenCalled();
      expect(keyDown).not.toHaveBeenCalled();
      expect(submit).not.toHaveBeenCalled();

      fireEvent.change(input, { target: { value: 'Updated draft\nsecond line' } });
      expect(changed).toHaveBeenCalledWith('Updated draft\nsecond line');
      expect(input).toHaveValue('Updated draft\nsecond line');
      fireEvent.keyDown(input, { key: 'Enter', shiftKey: true });
      expect(keyDown).toHaveBeenCalledTimes(1);
      expect(keyDown.mock.calls[0][0]).toMatchObject({ key: 'Enter', shiftKey: true });
      fireEvent.keyDown(input, { key: 'Enter' });
      expect(keyDown).toHaveBeenCalledTimes(2);
      expect(keyDown.mock.calls[1][0]).toMatchObject({ key: 'Enter', shiftKey: false });
      fireEvent.click(help);
      expect(screen.getByRole('dialog', { name: label })).toBeInTheDocument();
      view.unmount();
      await waitFor(() => expect(screen.queryByRole('dialog', { name: label })).not.toBeInTheDocument());
      expect(submit).not.toHaveBeenCalled();
    }
  );
});
