# Excerpt of hackclub/hackatime app/models/user.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob b492014cc09a88d6fcca0cf38501169a0a3f8345).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/models/user.rb lines 48-59 ----
  enum :trust_level, {
    blue: 0,     # unscored
    red: 1,      # convicted
    green: 2,    # trusted
    yellow: 3    # suspected (invisible to user)
  }

  def self.mask_trust_level(level)
    level.to_s == "yellow" ? "blue" : level.to_s
  end

  def public_trust_level = self.class.mask_trust_level(trust_level)

# ---- app/models/user.rb lines 95-112 ----
  # Look up a user by numeric ID, slack_uid, hca_id, or username
  def self.lookup_by_identifier(id)
    return nil if id.blank?

    numeric_id = id.to_i if id.match?(/^\d+$/)
    relation = where(slack_uid: id).or(where(hca_id: id)).or(where(username: id))
    relation = where(id: numeric_id).or(relation) if numeric_id

    candidates = relation.to_a
    lookup_order = [
      numeric_id && ->(u) { u.id == numeric_id },
      ->(u) { u.slack_uid == id },
      ->(u) { u.hca_id == id },
      ->(u) { u.username == id }
    ].compact
    lookup_order.each { |matcher| match = candidates.find(&matcher); return match if match }
    nil
  end
