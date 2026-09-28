import { describe, expect, it } from 'vitest';
import { splitDeliveryListPresentation } from '@/renderer/pages/conversation/Messages/components/deliveryListPresentation';

describe('historical delivery list presentation', () => {
  it('folds only an all-artifact trailing inventory while retaining the complete original text', () => {
    const appendix = '交付文件\n\n- [a.csv]({{artifact:version-a}})\n- [b.csv](/api/artifacts/b/versions/v2)';
    const original = 'Scientific result unchanged.\n\n' + appendix;
    const view = splitDeliveryListPresentation(original);
    expect(view).toEqual({
      body: 'Scientific result unchanged.',
      appendix,
      count: 2,
    });
    expect(view.body + '\n\n' + view.appendix).toBe(original);
  });
  it('leaves prose, user references and non-artifact links intact', () => {
    for (const content of [
      'No appendix',
      'Result\n\n交付文件\n\n- [a](https://example.org)\n- [b]({{artifact:v}})',
      'Result\n\nDeliverables\n\n- [a]({{artifact:v}})\nImportant limitation.',
    ])
      expect(splitDeliveryListPresentation(content)).toEqual({
        body: content,
        appendix: '',
        count: 0,
      });
  });
});
