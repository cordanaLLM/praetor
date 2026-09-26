// A struct pattern's braces belong to the pattern: the field it binds is in scope from the
// semicolon, head brace, guard, arrow or pipe after them. A later arm on the match's own line
// binds as a first arm does.
pub struct S {
    pub pick: fn() -> u8,
    pub other: u8,
}

pub fn pick(s: S) -> u8 {
    let S { pick, .. } = s;
    pick()
}

pub mod head {
    pub fn pick(s: super::S) -> u8 {
        if let super::S { pick, other: 0 } = s { pick() } else { 0 }
    }
}

pub mod arm {
    pub fn pick(s: super::S) -> u8 {
        match s {
            super::S { pick, other: 0 } if pick() > 0 => pick(),
            super::S { other, .. } => other,
        }
    }
}

pub mod closure {
    pub fn pick(v: &[super::S]) -> u8 {
        v.iter().map(|super::S { pick, .. }| pick()).sum()
    }
}

pub fn later(slot: Option<fn() -> u8>) -> u8 {
    match slot { None => 0, Some(later) => later() }
}
