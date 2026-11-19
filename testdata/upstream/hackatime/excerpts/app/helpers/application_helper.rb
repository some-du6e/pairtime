# Excerpt of hackclub/hackatime app/helpers/application_helper.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob cf630cc550ba9c1d407a50d5d61aa3ffa49606d0).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/helpers/application_helper.rb lines 40-56 ----
  def digital_time(time)
    h, m, s = time.to_i.divmod(3600).then { |h, r| [ h, *r.divmod(60) ] }
    [ h, m, s ].map { |part| part.to_s.rjust(2, "0") }.join(":")
  end

  def short_time_simple(time)
    hours = time.to_i / 3600
    minutes = (time.to_i % 3600) / 60
    return "0m" if hours.zero? && minutes.zero?
    [ hours.positive? && "#{hours}h", minutes.positive? && "#{minutes}m" ].select { |p| p }.join(" ")
  end

  def short_time_detailed(time)
    # ie. 5h 10m 10s / 10m 10s / 10m
    h, m, s = time.to_i.divmod(3600).then { |h, r| [ h, *r.divmod(60) ] }
    [ h.positive? && "#{h}h", m.positive? && "#{m}m", s.positive? && "#{s}s" ].select { |p| p }.join(" ")
  end
