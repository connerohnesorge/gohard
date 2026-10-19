package p is
  type distance is range 0 to 1e9
    units
      um;
      mm = 1000 um;
      m = 1000 mm;
    end units distance;
end;
