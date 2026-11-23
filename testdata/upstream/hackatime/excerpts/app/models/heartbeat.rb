# Excerpt of hackclub/hackatime app/models/heartbeat.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 5bcd4412a8e81ce287be756b971831cdf15566d0).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/models/heartbeat.rb lines 41-56 ----
  def self.generate_fields_hash(attributes)
    attributes = attributes.transform_keys(&:to_s)
    indexed = attributes.slice(*indexed_attributes)
    ai_indexed_attributes.each do |attribute|
      indexed[attribute] = attributes[attribute] unless attributes[attribute].nil?
    end
    Digest::MD5.hexdigest(indexed.to_json)
  end

  def self.indexed_attributes
    %w[user_id branch category dependencies editor entity language machine operating_system project type user_agent line_additions line_deletions lineno lines cursorpos project_root_count time is_write]
  end

  def self.ai_indexed_attributes
    %w[ai_model ai_session ai_subscription_plan ai_input_tokens ai_output_tokens ai_prompt_length ai_line_changes human_line_changes]
  end
