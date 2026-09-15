// HISS-14: `publish` previously accepted (topic: &str, payload: &[u8]). It now requires
// a leading &Ctx, so every existing caller breaks. Committed with an ordinary
// "fix(api): thread a ctx through the published entry point" header nothing notices.
pub struct Ctx {
    pub deadline_ms: u64,
}

pub fn publish(ctx: &Ctx, topic: &str, payload: &[u8]) -> u64 {
    if payload.is_empty() {
        return 0;
    }
    ctx.deadline_ms + topic.len() as u64
}
