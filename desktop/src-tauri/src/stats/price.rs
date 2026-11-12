/// USD per million tokens.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Rates {
    pub input: f64,
    pub output: f64,
    pub cache_write_5m: f64,
    pub cache_write_1h: f64,
    pub cache_read: f64,
}

/// A request's token counts, as read from `message.usage`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct RequestTokens {
    pub input: u64,
    pub output: u64,
    pub thinking: u64,
    pub cache_write_5m: u64,
    pub cache_write_1h: u64,
    pub cache_read: u64,
}

/// Prompts above this many tokens use Haiku 5.5's long-context rates.
pub const HAIKU_LONG_PROMPT: u64 = 100_000;

const HAIKU_PREFIX: &str = "claude-haiku-5-5";

struct Price {
    prefix: &'static str,
    rates: Rates,
}

const PRICES: &[Price] = &[
    Price {
        prefix: "claude-opus-5-5",
        rates: Rates {
            input: 4.0,
            output: 20.0,
            cache_write_5m: 5.0,
            cache_write_1h: 8.0,
            cache_read: 0.20,
        },
    },
    Price {
        prefix: "claude-sonnet-5-5",
        rates: Rates {
            input: 2.0,
            output: 10.0,
            cache_write_5m: 2.5,
            cache_write_1h: 4.0,
            cache_read: 0.10,
        },
    },
    Price {
        prefix: HAIKU_PREFIX,
        rates: Rates {
            input: 0.10,
            output: 0.50,
            cache_write_5m: 0.125,
            cache_write_1h: 0.20,
            cache_read: 0.01,
        },
    },
    Price {
        prefix: "claude-fable-5-2",
        rates: Rates {
            input: 8.0,
            output: 40.0,
            cache_write_5m: 10.0,
            cache_write_1h: 16.0,
            cache_read: 0.20,
        },
    },
];

const HAIKU_LONG_CONTEXT: Rates = Rates {
    input: 0.50,
    output: 2.50,
    cache_write_5m: 0.625,
    cache_write_1h: 1.00,
    cache_read: 0.05,
};

const FAMILIES: [&str; 5] = ["opus", "sonnet", "haiku", "fable", "mythos"];

/// The rates for model at this prompt size (input + cache read + cache
/// writes), or None when no rate is known.
pub fn rates_for(model: &str, prompt_tokens: u64) -> Option<Rates> {
    let price = PRICES
        .iter()
        .find(|price| model.starts_with(price.prefix))?;
    if price.prefix == HAIKU_PREFIX && prompt_tokens > HAIKU_LONG_PROMPT {
        return Some(HAIKU_LONG_CONTEXT);
    }
    Some(price.rates)
}

/// The estimated cost in USD of one request; 0.0 when the model has no rates.
pub fn request_cost(model: &str, tokens: &RequestTokens) -> f64 {
    let prompt = tokens
        .input
        .saturating_add(tokens.cache_read)
        .saturating_add(tokens.cache_write_5m)
        .saturating_add(tokens.cache_write_1h);
    let Some(rates) = rates_for(model, prompt) else {
        return 0.0;
    };
    per_million(tokens.input, rates.input)
        + per_million(tokens.output, rates.output)
        + per_million(tokens.cache_write_5m, rates.cache_write_5m)
        + per_million(tokens.cache_write_1h, rates.cache_write_1h)
        + per_million(tokens.cache_read, rates.cache_read)
}

/// "opus", "sonnet", "haiku", "fable", "mythos" when the model id contains
/// one of them, else "other".
pub fn family(model: &str) -> &'static str {
    FAMILIES
        .iter()
        .copied()
        .find(|family| model.contains(family))
        .unwrap_or("other")
}

/// A display name: "claude-haiku-5-5" → "Haiku 5.5"; the model id unchanged
/// when it isn't `claude-<word>-<digits>…`.
pub fn label(model: &str) -> String {
    let Some(rest) = model.strip_prefix("claude-") else {
        return model.to_string();
    };
    let mut parts = rest.split('-');
    let name = parts.next().unwrap_or_default();
    let versions: Vec<&str> = parts.collect();
    let is_word = !name.is_empty() && name.bytes().all(|b| b.is_ascii_alphabetic());
    let is_version = |part: &&str| !part.is_empty() && part.bytes().all(|b| b.is_ascii_digit());
    if !is_word || versions.is_empty() || !versions.iter().all(is_version) {
        return model.to_string();
    }
    let (first, others) = name.split_at(1);
    format!(
        "{}{} {}",
        first.to_ascii_uppercase(),
        others,
        versions.join(".")
    )
}

fn per_million(tokens: u64, usd_per_million: f64) -> f64 {
    tokens as f64 * usd_per_million / 1_000_000.0
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sonnet_tokens_of_one_million_each() -> RequestTokens {
        RequestTokens {
            input: 1_000_000,
            output: 1_000_000,
            thinking: 0,
            cache_write_5m: 1_000_000,
            cache_write_1h: 1_000_000,
            cache_read: 1_000_000,
        }
    }

    #[test]
    fn opus_has_its_own_rates() {
        assert_eq!(
            rates_for("claude-opus-5-5", 0).map(|r| r.output),
            Some(20.0)
        );
    }

    #[test]
    fn sonnet_has_its_own_rates() {
        assert_eq!(
            rates_for("claude-sonnet-5-5", 0).map(|r| r.cache_read),
            Some(0.10)
        );
    }

    #[test]
    fn fable_has_its_own_rates() {
        assert_eq!(
            rates_for("claude-fable-5-2", 0).map(|r| r.cache_write_1h),
            Some(16.0)
        );
    }

    #[test]
    fn haiku_at_exactly_the_threshold_uses_short_rates() {
        assert_eq!(
            rates_for("claude-haiku-5-5", HAIKU_LONG_PROMPT).map(|r| r.input),
            Some(0.10)
        );
    }

    #[test]
    fn haiku_above_the_threshold_uses_long_context_rates() {
        assert_eq!(
            rates_for("claude-haiku-5-5", HAIKU_LONG_PROMPT + 1).map(|r| r.input),
            Some(0.50)
        );
    }

    #[test]
    fn unknown_model_has_no_rates() {
        assert_eq!(rates_for("claude-mythos-9-9", 0), None);
    }

    #[test]
    fn unknown_model_costs_nothing() {
        assert_eq!(
            request_cost("claude-mythos-9-9", &sonnet_tokens_of_one_million_each()),
            0.0
        );
    }

    #[test]
    fn request_cost_sums_each_token_kind_at_its_rate() {
        let cost = request_cost("claude-sonnet-5-5", &sonnet_tokens_of_one_million_each());
        assert!((cost - 18.6).abs() < 1e-9, "cost was {cost}");
    }

    #[test]
    fn thinking_tokens_are_not_priced_separately() {
        let tokens = RequestTokens {
            thinking: 1_000_000,
            ..RequestTokens::default()
        };
        assert_eq!(request_cost("claude-sonnet-5-5", &tokens), 0.0);
    }

    #[test]
    fn family_of_each_known_word() {
        assert_eq!(family("claude-opus-5-5"), "opus");
        assert_eq!(family("claude-sonnet-5-5"), "sonnet");
        assert_eq!(family("claude-haiku-5-5"), "haiku");
        assert_eq!(family("claude-fable-5-2"), "fable");
        assert_eq!(family("claude-mythos-1-0"), "mythos");
    }

    #[test]
    fn family_of_other_model_is_other() {
        assert_eq!(family("<synthetic>"), "other");
    }

    #[test]
    fn label_of_current_model_id() {
        assert_eq!(label("claude-haiku-5-5"), "Haiku 5.5");
    }

    #[test]
    fn label_of_older_model_id() {
        assert_eq!(label("claude-opus-4-8"), "Opus 4.8");
    }

    #[test]
    fn label_of_dated_model_id_is_unchanged() {
        assert_eq!(
            label("claude-3-5-sonnet-20241022"),
            "claude-3-5-sonnet-20241022"
        );
    }

    #[test]
    fn label_of_synthetic_model_is_unchanged() {
        assert_eq!(label("<synthetic>"), "<synthetic>");
    }
}
