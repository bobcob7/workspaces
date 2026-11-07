//! The IPC types commands take: Tauri's under `desktop`, look-alikes that
//! `online::dispatch` builds under `online` (ADR 0053).

#[cfg(feature = "desktop")]
pub use tauri::{ipc::Channel, State};

#[cfg(not(feature = "desktop"))]
#[allow(unused_imports)]
pub use shim::{Channel, ChannelClosed, State};

#[cfg(not(feature = "desktop"))]
mod shim {
    use serde::Serialize;
    use serde_json::Value;
    use std::fmt;
    use std::marker::PhantomData;
    use std::ops::Deref;
    use std::sync::Arc;

    /// Borrows managed state for a command, as Tauri's `State` does.
    pub struct State<'r, T: Send + Sync + 'static>(&'r T);

    impl<'r, T: Send + Sync + 'static> State<'r, T> {
        pub fn new(inner: &'r T) -> Self {
            Self(inner)
        }
    }

    impl<T: Send + Sync + 'static> Deref for State<'_, T> {
        type Target = T;

        fn deref(&self) -> &T {
            self.0
        }
    }

    impl<T: Send + Sync + 'static> Clone for State<'_, T> {
        fn clone(&self) -> Self {
            *self
        }
    }

    impl<T: Send + Sync + 'static> Copy for State<'_, T> {}

    /// The channel's receiver is gone.
    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub struct ChannelClosed;

    impl fmt::Display for ChannelClosed {
        fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
            f.write_str("channel closed")
        }
    }

    impl std::error::Error for ChannelClosed {}

    /// Sends serialized messages of type `T` to a sink, as Tauri's `Channel`
    /// does.
    pub struct Channel<T> {
        sink: Arc<dyn Fn(Value) -> Result<(), ChannelClosed> + Send + Sync>,
        _message: PhantomData<fn(T)>,
    }

    impl<T> Clone for Channel<T> {
        fn clone(&self) -> Self {
            Self {
                sink: Arc::clone(&self.sink),
                _message: PhantomData,
            }
        }
    }

    impl<T: Serialize> Channel<T> {
        pub fn new(
            sink: impl Fn(Value) -> Result<(), ChannelClosed> + Send + Sync + 'static,
        ) -> Self {
            Self {
                sink: Arc::new(sink),
                _message: PhantomData,
            }
        }

        pub fn send(&self, message: T) -> Result<(), ChannelClosed> {
            let value = serde_json::to_value(message).map_err(|_| ChannelClosed)?;
            (self.sink)(value)
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;
        use std::sync::Mutex;

        #[test]
        fn state_derefs_to_inner() {
            let inner = 5_u32;
            let state = State::new(&inner);
            let copy = state;
            assert_eq!((*state, *copy), (5, 5));
        }

        #[test]
        fn channel_sends_serialized_messages() {
            let sent = Arc::new(Mutex::new(Vec::new()));
            let sink = Arc::clone(&sent);
            let channel = Channel::new(move |value| {
                sink.lock().unwrap().push(value);
                Ok(())
            });
            channel.send(serde_json::json!({"n": 1})).unwrap();
            channel.clone().send(serde_json::json!({"n": 2})).unwrap();
            assert_eq!(
                *sent.lock().unwrap(),
                vec![serde_json::json!({"n": 1}), serde_json::json!({"n": 2})]
            );
        }

        #[test]
        fn channel_reports_closed_sink() {
            let channel: Channel<Value> = Channel::new(|_| Err(ChannelClosed));
            assert_eq!(channel.send(Value::Null), Err(ChannelClosed));
        }
    }
}
