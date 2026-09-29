import "@arco-design/web-react/es/_util/react-19-adapter";
import "@arco-design/web-react/dist/css/arco.css";
import "uno.css";
import "@/renderer/styles/themes/index.css";
import "@/renderer/styles/workspace-theme.css";
import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { HashRouter, Routes, Route, useParams } from "react-router";
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import zhMessages from "@/renderer/services/i18n/locales/zh-CN/messages.json";
import MessageReplyBranchButton from "@/renderer/pages/conversation/Messages/components/MessageReplyBranchButton";
import MessageRoundFooter from "@/renderer/pages/conversation/Messages/components/MessageRoundFooter";
import {
  decodeRoundSummary,
  type RoundSummary,
} from "@/common/chat/roundSummary";
import { ScientificFilePreviewStrip } from "@/renderer/pages/conversation/Messages/components/MessageScientificFiles";
import { PreviewProvider } from "@/renderer/pages/conversation/Preview/context/PreviewContext";
import type { ISynonBiomedScientificFile } from "@/common/adapter/ipcBridge";

await i18n.use(initReactI18next).init({
  lng: "zh-CN",
  resources: { "zh-CN": { translation: { messages: zhMessages } } },
  interpolation: { escapeValue: false },
});
const fixture: {
  source: string;
  attempt: number;
  artifact: { artifact_id: string; version_id: string };
} = await fetch("/__branch_fixture__").then((r) => r.json());
function Page() {
  const { id = "" } = useParams();
  const [history, setHistory] = useState("");
  const [summary, setSummary] = useState<RoundSummary>();
  const [files, setFiles] = useState<ISynonBiomedScientificFile[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    setHistory("");
    setSummary(undefined);
    setFiles([]);
    fetch(`/api/conversations/${encodeURIComponent(id)}/messages?limit=80`, {
      signal: controller.signal,
    })
      .then(async (r) => {
        if (!r.ok) throw new Error("History request failed");
        return r.json();
      })
      .then(async (data) => {
        setHistory(JSON.stringify(data));
        const messages = data.items ?? data.messages ?? data;
        setSummary(
          messages
            .map((message: { round_summary?: unknown }) =>
              decodeRoundSummary(message.round_summary)
            )
            .find(
              (value: RoundSummary | undefined) =>
                value?.attempt === fixture.attempt
            )
        );
        const response = await fetch(
          `/api/conversations/${encodeURIComponent(id)}/artifacts`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              references: [
                {
                  artifact_id: fixture.artifact.artifact_id,
                  version_id: fixture.artifact.version_id,
                },
              ],
            }),
            signal: controller.signal,
          }
        );
        if (!response.ok) throw new Error("Artifact metadata request failed");
        const artifacts = await response.json();
        if (!controller.signal.aborted)
          setFiles(
            artifacts.flatMap(
              (artifact: {
                payload: { files: ISynonBiomedScientificFile[] };
              }) => artifact.payload.files
            )
          );
      })
      .catch((error) => {
        if (!controller.signal.aborted) setHistory(String(error));
      });
    return () => controller.abort();
  }, [id]);
  return (
    <main style={{ padding: 30 }}>
      <p>Controlled reply branch regression fixture, not scientific output.</p>
      <p data-testid="conversation-id">{id}</p>
      <pre data-testid="history" style={{ whiteSpace: "pre-wrap" }}>
        {history}
      </pre>
      {id === fixture.source && (
        <MessageReplyBranchButton
          conversationId={id}
          throughAttempt={fixture.attempt}
        />
      )}
      {summary && <MessageRoundFooter summary={summary} />}
      <PreviewProvider>
        <ScientificFilePreviewStrip files={files} inline />
      </PreviewProvider>
    </main>
  );
}
createRoot(document.getElementById("root")!).render(
  <HashRouter>
    <Routes>
      <Route path="/conversation/:id" element={<Page />} />
    </Routes>
  </HashRouter>
);
