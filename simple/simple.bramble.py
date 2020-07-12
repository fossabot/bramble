load("../seed", "seed")

derivation(
    name="simple",
    environment={"seed": seed},
    builder="%s/bin/sh" % seed,
    args=["$src/simple_builder.sh"],
    sources=["./simple.c", "simple_builder.sh"],
)
