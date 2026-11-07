import { Button, Stack, Typography } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import { Row } from "../features/settings/Row";
import { disablePush, enablePush, pushState, sendTestPush, type PushState, type TestResult } from "./push";

const helpByState: Record<PushState, string> = {
  subscribed: "Notifications reach this browser even when the app is closed.",
  granted: "Turn on to get notifications here when the app is closed.",
  default: "Turn on to get notifications here when the app is closed.",
  denied: "Blocked in the browser's site settings.",
  unsupported: "This browser can't receive push notifications.",
  insecure: "Needs HTTPS (or localhost). See docs/online.md.",
};

const resultMessage = ({ sent, failed }: TestResult): string => {
  const devices = `Sent to ${sent} ${sent === 1 ? "device" : "devices"}.`;
  return failed === 0 ? devices : `${devices} ${failed} couldn't be reached.`;
};

/** The This browser row under Settings › Notifications: turns push on or off here and sends a test. */
export const BrowserPushRow = (): JSX.Element => {
  const [state, setState] = useState<PushState | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const [result, setResult] = useState<string | null>(null);

  useEffect(() => {
    void pushState().then(setState);
  }, []);

  const run = async (action: () => Promise<void>, failure: string): Promise<void> => {
    setBusy(true);
    setError(undefined);
    try {
      await action();
    } catch {
      setError(failure);
    } finally {
      setBusy(false);
    }
  };

  const turnOn = (): Promise<void> =>
    run(async () => {
      setResult(null);
      setState(await enablePush());
    }, "Couldn't turn on notifications.");

  const turnOff = (): Promise<void> =>
    run(async () => {
      setResult(null);
      setState(await disablePush());
    }, "Couldn't turn off notifications.");

  const sendTest = (): Promise<void> =>
    run(async () => setResult(resultMessage(await sendTestPush())), "Couldn't send the test.");

  const canTurnOn = state === "default" || state === "granted";
  const subscribed = state === "subscribed";

  return (
    <Row label="This browser" help={state === null ? undefined : helpByState[state]} errors={[error]}>
      {(canTurnOn || subscribed) && (
        <Stack direction="row" spacing={1}>
          {canTurnOn && (
            <Button size="small" variant="contained" disabled={busy} onClick={() => void turnOn()}>
              Turn on
            </Button>
          )}
          {subscribed && (
            <>
              <Button size="small" disabled={busy} onClick={() => void turnOff()}>
                Turn off
              </Button>
              <Button size="small" disabled={busy} onClick={() => void sendTest()}>
                Send a test
              </Button>
            </>
          )}
        </Stack>
      )}
      {result !== null && (
        <Typography variant="body2" color="text.secondary" role="status">
          {result}
        </Typography>
      )}
    </Row>
  );
};
