# Excerpt of hackclub/hackatime app/services/heartbeat_ingest.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob fe4d313f321377e42bc9d9f02099c7bc0218e476).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/services/heartbeat_ingest.rb lines 17-18 ----
  Result = Data.define(:total_count, :persisted_count, :duplicate_count, :failed_count, :errors, :items)
  Item = Data.define(:heartbeat, :status, :error)

# ---- app/services/heartbeat_ingest.rb lines 42-100 ----
  def ingest_direct
    items = Array.new(@heartbeats.length)
    errors = []
    entries = []
    persisted_count = duplicate_count = 0
    placeholder_state = { contexts: {}, last_project: nil }

    @heartbeats.each_with_index do |heartbeat, index|
      model_attributes = validated_model_attributes(normalize_direct_heartbeat(heartbeat, placeholder_state:))
      if (acode = acode_heartbeat(heartbeat, category: model_attributes["category"]))
        legacy_fields_hash = Heartbeat.generate_fields_hash(model_attributes)
        model_attributes = validated_model_attributes(normalize_direct_heartbeat(acode, placeholder_state:))
      end
      attrs = model_attributes.symbolize_keys
      update_placeholder_state!(attrs, placeholder_state)
      entries << {
        index:,
        heartbeat:,
        attrs:,
        model_attributes:,
        legacy_fields_hash:,
        fields_hash: Heartbeat.generate_fields_hash(model_attributes)
      }
    rescue => e
      errors << { heartbeat: heartbeat, error: e.message, type: e.class.name }
      items[index] = Item.new(heartbeat: nil, status: :failed, error: e)
    end

    if entries.any?
      begin
        persisted_by_hash, inserted_hashes = persist_direct_heartbeats(entries)
        persisted_count = inserted_hashes.length
        duplicate_count = entries.length - persisted_count

        entries.each do |entry|
          items[entry[:index]] = Item.new(
            heartbeat: persisted_by_hash.fetch(entry[:fields_hash]),
            status: :accepted,
            error: nil
          )
        end
        entries.map { |entry| entry[:attrs][:project] }.uniq.each { |project| queue_project_mapping(project) }
      rescue => e
        entries.each do |entry|
          errors << { heartbeat: entry[:heartbeat], error: e.message, type: e.class.name }
          items[entry[:index]] = Item.new(heartbeat: nil, status: :failed, error: e)
        end
      end
    end

    Result.new(
      total_count: @heartbeats.length,
      persisted_count:,
      duplicate_count:,
      failed_count: errors.length,
      errors:,
      items:
    )
  end

# ---- app/services/heartbeat_ingest.rb lines 364-395 ----
  def normalize_epoch_time(value)
    t = Float(value)
    raise InvalidHeartbeatTime unless t.finite?
    return t if t >= EPOCH_SANE_MIN && t < EPOCH_SANE_MAX

    # ms / µs / ns-scaled epochs are mechanically repairable
    [ 1e3, 1e6, 1e9 ].each do |scale|
      scaled = t / scale
      return scaled if scaled >= EPOCH_SANE_MIN && scaled < EPOCH_SANE_MAX
    end

    # Seconds counted from 1601 instead of 1970. No scaled epoch lands in this range.
    unix = t - WINDOWS_EPOCH_OFFSET
    return unix if unix >= EPOCH_SANE_MIN && unix < EPOCH_SANE_MAX

    raise InvalidHeartbeatTime, "time must be Unix epoch seconds between #{EPOCH_SANE_MIN} and #{EPOCH_SANE_MAX - 1}"
  rescue TypeError, ArgumentError => e
    raise e if e.is_a?(InvalidHeartbeatTime)
    raise InvalidHeartbeatTime, "time must be a Unix epoch timestamp"
  end

  def normalize_heartbeat_time(value)
    if value.is_a?(String) && !value.strip.match?(/\A[+-]?(?:\d+(?:\.\d*)?|\.\d+)\z/)
      normalize_epoch_time(Time.parse(value).to_f)
    else
      normalize_epoch_time(value)
    end
  rescue InvalidHeartbeatTime
    raise
  rescue TypeError, ArgumentError
    raise InvalidHeartbeatTime, "time must be a Unix epoch timestamp or parseable date"
  end

# ---- app/services/heartbeat_ingest.rb lines 412-417 ----
  def default_category(category, type:)
    return category if category.present?
    return "browsing" if %w[domain url].include?(type)

    "coding"
  end
