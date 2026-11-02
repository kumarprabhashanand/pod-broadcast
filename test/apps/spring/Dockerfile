FROM maven:3-eclipse-temurin-27@sha256:bd2c0a2c430f787b7b6887826b6b1a43fe48f1e3ca4196b9ab886567bf54de58 AS build
WORKDIR /src
COPY pom.xml .
COPY src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -B -q package -DskipTests

FROM eclipse-temurin:25-jre@sha256:ecdc0adb37bde1a281c4d245a3cd535004b1fc8322d560f5b09463b248c81df0
COPY --from=build /src/target/sample-0.0.1-SNAPSHOT.jar /app.jar
USER 65532:65532
ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75 -XX:+UseSerialGC -XX:TieredStopAtLevel=1"
ENTRYPOINT ["java", "-jar", "/app.jar"]
