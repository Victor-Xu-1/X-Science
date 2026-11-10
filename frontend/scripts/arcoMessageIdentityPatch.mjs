import { createHash } from 'node:crypto';

export const ARCO_MESSAGE_VERSION = '2.66.8';

// Arco 2.66.8 (MIT), components/Message/useMessage.tsx. Remove this narrow
// compatibility patch once upstream assigns container identities and binds
// returned close handles to the receiving instance.
const formats = {
  es: {
    ref: 'useRef',
    create: 'React.createElement(Message,',
    original: '4cf53f1dcdd7753a54d419195d177f9f3d320e0878453cfa4def01b4baf6bff9',
    prepared: 'a515b8732aad1a3a1237cf26d9c66b81a29972530603ab31ba3904323595e758',
  },
  lib: {
    ref: '(0, react_1.useRef)',
    create: 'react_1.default.createElement(_1.default,',
    original: '3beb78acf1becd32cdd0b59f08e7130c74e8885b82deacc77722e0d24fd90315',
    prepared: '1702a87e096f90da336a805078a80ad7f5f197309e6b90e371123abaf68090f5',
  },
};

/** @param {string} source */
export function sourceSha256(source) {
  return createHash('sha256').update(source).digest('hex');
}

/**
 * Patch only the locked upstream bytes; reject changed/partially patched input.
 * The counter identifies container registrations, not position or notice order.
 * Notice state, limits, duration and rendering stay in the original Arco code.
 * @param {string} source
 * @param {'es' | 'lib'} format
 */
export function patchArcoMessageSource(source, format) {
  const spec = formats[format];
  if (!spec) throw new Error('Unsupported Arco Message module format.');
  const before = sourceSha256(source);
  if (before === spec.prepared) return source;
  if (before !== spec.original) {
    throw new Error('Arco Message source changed; review the locked compatibility patch.');
  }

  const replacements = [
    [
      '    var contextHolderRef = ' + spec.ref + '();',
      '    var contextHolderRef = ' + spec.ref + '();\n    var messageContainerSequence = ' + spec.ref + '(0);',
    ],
    [
      'notice = (' + spec.create + ' { transitionClassNames:',
      'notice = (' +
        spec.create +
        " { key: 'arco-message-container-' + messageContainerSequence.current++, transitionClassNames:",
    ],
    [
      '        var id;',
      '        var id;\n        var noticeInstance = messageInstance[position];\n        var closed = false;',
    ],
    [
      '                    messageInstance[position] = instance;\n                    if (messageInstance[position]) {\n                        id = messageInstance[position].add(_noticeProps);',
      '                    messageInstance[position] = instance;\n                    noticeInstance = instance;\n                    if (noticeInstance && !closed) {\n                        id = noticeInstance.add(_noticeProps);',
    ],
    [
      '        var close = function () {\n            if (messageInstance[position]) {\n                messageInstance[position].remove(id);\n            }\n        };',
      '        var close = function () {\n            closed = true;\n            if (contextHolderRef.current && noticeInstance) {\n                noticeInstance.remove(id);\n            }\n        };',
    ],
  ];

  let prepared = source;
  for (const [beforeText, afterText] of replacements) {
    if (prepared.split(beforeText).length !== 2) throw new Error('Arco Message patch anchor changed.');
    prepared = prepared.replace(beforeText, afterText);
  }
  if (sourceSha256(prepared) !== spec.prepared) throw new Error('Arco Message patch output changed.');
  return prepared;
}
